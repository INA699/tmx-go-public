// Package ownerpin is the owner PIN: one short code for every owner step a browser takes, in
// the same record format as the Python daemon's (src/tmx/gate.py, "the owner PIN").
//
// Record (<TMX_HOME>/secrets/owner-pin.json): {"salt": 32 hex (16 bytes), "hash": 64 hex,
// "set": unix seconds}. hash = scrypt(PIN as UTF-8, salt, N=16384, r=8, p=1, 32 bytes).
// The record never leaves the machine: it is not synced between the fleet's machines (a
// leaked salt+hash of a 6-digit PIN is brute-forced offline in minutes), so each machine's PIN
// is set at that machine.
//
// Wrong PINs: each source (one paired device, one host's pairing browsers, the local UI) waits
// after 5 wrong in a row; and all sources together share one count that no new source, cookie
// or UI nonce resets and that survives a restart (<TMX_HOME>/secrets/owner-pin-tries.json):
// from the 5th wrong PIN in a row, anywhere, every PIN waits 1 min, then 2, 4, ... up to 4 h.
// The right PIN, or the owner at the machine (Clear wrong-PIN waits), resets it.
package ownerpin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

const (
	scryptN, scryptR, scryptP, keyLen = 16384, 8, 1, 32
	MinLen, MaxLen                    = 6, 12
	// FutureSkew: a record set further ahead than this is not valid (Python: 300 s).
	FutureSkew = 300 * time.Second
	// Session and approval limits, the same as Python's.
	SessionIdle = time.Hour
	SessionMax  = 12 * time.Hour
	Fresh       = 5 * time.Minute
)

var (
	ErrFormat  = errors.New("the PIN is 6 to 12 digits")
	ErrWrong   = errors.New("wrong PIN")
	ErrNotSet  = errors.New("no owner PIN is set yet: set it at the machine")
	ErrInvalid = errors.New("invalid PIN record")
	// ErrBusy: a check from this source is already running, or MaxChecks are: try again in a
	// second. Never a guess: the PIN was not checked.
	ErrBusy = errors.New("a PIN check is already running: try again in a second")
)

// MaxChecks bounds concurrent scrypt checks (about 16 MB each) across all sources.
const MaxChecks = 4

// The machine-wide lockout: GlobalFree wrong PINs in a row (from any source) are free; from
// then on every try waits GlobalFirstWait, doubling with each further wrong PIN, up to
// GlobalMaxWait. A 6-digit PIN then takes centuries to guess.
const (
	GlobalFree      = 5
	GlobalFirstWait = time.Minute
	GlobalMaxWait   = 4 * time.Hour
)

// globalWait is the lockout after n wrong PINs in a row (0 below GlobalFree).
func globalWait(n int) time.Duration {
	if n < GlobalFree {
		return 0
	}
	d := GlobalFirstWait
	for i := GlobalFree; i < n && d < GlobalMaxWait; i++ {
		d *= 2
	}
	return min(d, GlobalMaxWait)
}

// Record is the stored PIN, field for field the Python record.
type Record struct {
	Salt string  `json:"salt"`
	Hash string  `json:"hash"`
	Set  float64 `json:"set"`
}

// SetTime is when the PIN was set.
func (r Record) SetTime() time.Time {
	sec, frac := math.Modf(r.Set)
	return time.Unix(int64(sec), int64(frac*1e9))
}

// Valid checks the shape (salt 32 hex, hash 64 hex, set not too far ahead of now).
func (r Record) Valid(now time.Time) bool {
	s, err1 := hex.DecodeString(r.Salt)
	h, err2 := hex.DecodeString(r.Hash)
	return err1 == nil && err2 == nil && len(s) == 16 && len(h) == keyLen && r.Set > 0 &&
		!math.IsInf(r.Set, 0) && !math.IsNaN(r.Set) && !r.SetTime().After(now.Add(FutureSkew))
}

// ValidPIN: digits only, 6 to 12.
func ValidPIN(pin string) bool {
	if len(pin) < MinLen || len(pin) > MaxLen {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Hash derives the stored hash of pin with salt (raw bytes).
func Hash(pin string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(pin), salt, scryptN, scryptR, scryptP, keyLen)
}

// NewRecord hashes pin with a fresh random salt.
func NewRecord(pin string, now time.Time) (Record, error) {
	if !ValidPIN(pin) {
		return Record{}, ErrFormat
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Record{}, err
	}
	h, err := Hash(pin, salt)
	if err != nil {
		return Record{}, err
	}
	return Record{Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(h), Set: float64(now.UnixNano()) / 1e9}, nil
}

// Check compares pin against r in constant time.
func (r Record) Check(pin string) bool {
	if !ValidPIN(pin) {
		return false
	}
	salt, err := hex.DecodeString(r.Salt)
	if err != nil {
		return false
	}
	h, err := Hash(pin, salt)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h)), []byte(r.Hash)) == 1
}

// Store is the record on disk plus the in-memory backoff. Safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	path  string
	rec   *Record
	err   error
	Now   func() time.Time
	fails map[string]*failure
	// global is the machine-wide count (every source together), kept on disk at triesPath.
	global    failure
	triesPath string
	// One check in flight per source, MaxChecks in all: parallel requests can't each get a
	// guess before the count rises (review of #146), nor exhaust memory with scrypt.
	inflight map[string]bool
}

type failure struct {
	n     int
	until time.Time
}

// triesFile is the machine-wide wrong-PIN count on disk.
type triesFile struct {
	Fails int   `json:"fails"`
	Until int64 `json:"until"` // Unix ms; 0 = no wait
}

// TriesPath is where the machine-wide wrong-PIN count of the record at path is kept.
func TriesPath(path string) string {
	return filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), ".json")+"-tries.json")
}

// Open reads path (a missing file = no PIN yet) and the wrong-PIN count beside it.
func Open(path string) *Store {
	s := &Store{path: path, Now: time.Now, fails: map[string]*failure{}, inflight: map[string]bool{}, triesPath: TriesPath(path)}
	s.loadTries()
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		s.err = err
	default:
		var r Record
		if json.Unmarshal(b, &r) != nil || !r.Valid(time.Now().Add(100*365*24*time.Hour)) {
			s.err = ErrInvalid // never treated as "no PIN": that would let anyone set one
		} else {
			s.rec = &r
		}
	}
	return s
}

// Err is a load error (the PIN can't be used or set until it is fixed at the machine).
func (s *Store) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// IsSet tells whether a PIN exists.
func (s *Store) IsSet() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.rec != nil }

// Record returns a copy of the stored record, or nil.
func (s *Store) Record() *Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec == nil {
		return nil
	}
	r := *s.rec
	return &r
}

// caller holds mu
func (s *Store) write(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := config.WriteSecretFile(s.path, append(b, '\n')); err != nil {
		return err
	}
	s.rec, s.err = &r, nil
	return nil
}

// Set stores a new PIN. The caller decides who may (first time: the owner at the machine;
// a change: the old PIN or the owner at the machine). Returns the new record to push.
func (s *Store) Set(pin string) (Record, error) {
	r, err := NewRecord(pin, s.Now())
	if err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return r, s.write(r)
}

// loadTries reads the machine-wide count. A file that can't be read or parsed fails closed:
// it counts as GlobalFree wrong PINs (a wait), never as none.
func (s *Store) loadTries() {
	now := time.Now()
	b, err := os.ReadFile(s.triesPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var t triesFile
	if err != nil || json.Unmarshal(b, &t) != nil || t.Fails < 0 {
		s.global = failure{n: GlobalFree, until: now.Add(globalWait(GlobalFree))}
		return
	}
	s.global.n = t.Fails
	if t.Until > 0 {
		// A clock set back never makes the wait longer than the longest one.
		s.global.until = time.UnixMilli(t.Until)
		if lim := now.Add(GlobalMaxWait); s.global.until.After(lim) {
			s.global.until = lim
		}
	}
}

// saveTries writes the machine-wide count. Caller holds mu. A write error keeps the count in
// memory (it still applies until the next restart).
func (s *Store) saveTries() {
	t := triesFile{Fails: s.global.n}
	if !s.global.until.IsZero() {
		t.Until = s.global.until.UnixMilli()
	}
	b, _ := json.Marshal(t)
	_ = config.WriteSecretFile(s.triesPath, append(b, '\n'))
}

// Wait is how long source must wait before its next try (0 = now): its own wait or the
// machine-wide one, whichever is longer.
func (s *Store) Wait(source string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	d := s.global.until.Sub(now)
	if f := s.fails[source]; f != nil {
		d = max(d, f.until.Sub(now))
	}
	return max(d, 0)
}

// Verify checks pin for source with the backoff. Per source: wrong #5..#9 wait 60 s, #10 on
// 900 s each. Machine-wide: see GlobalFree. Never permanent. A right PIN clears the source and
// the machine-wide count. A waiting caller gets a *WaitError and its PIN is not checked at all.
func (s *Store) Verify(source, pin string) error {
	s.mu.Lock()
	rec, loadErr := s.rec, s.err
	f := s.fails[source]
	now := s.Now()
	if now.Before(s.global.until) {
		s.mu.Unlock()
		return &WaitError{s.global.until.Sub(now)}
	}
	if f != nil && now.Before(f.until) {
		s.mu.Unlock()
		return &WaitError{f.until.Sub(now)}
	}
	if loadErr != nil || rec == nil {
		s.mu.Unlock()
		if loadErr != nil {
			return loadErr
		}
		return ErrNotSet
	}
	// Past the free tries, one check at a time machine-wide: each wrong PIN sets the wait
	// before the next guess is checked, whichever source it comes from.
	if s.inflight[source] || len(s.inflight) >= MaxChecks ||
		len(s.inflight) > 0 && s.global.n+len(s.inflight) >= GlobalFree {
		s.mu.Unlock()
		return ErrBusy
	}
	s.inflight[source] = true // reserved under the lock: the count below is seen by the next try
	s.mu.Unlock()
	ok := rec.Check(pin) // slow: outside the lock
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, source)
	if ok {
		delete(s.fails, source)
		if s.global.n > 0 || !s.global.until.IsZero() {
			s.global = failure{}
			s.saveTries()
		}
		return nil
	}
	s.global.n++
	if d := globalWait(s.global.n); d > 0 {
		s.global.until = s.Now().Add(d)
	}
	s.saveTries()
	if s.fails[source] == nil {
		if len(s.fails) >= 10000 { // bound memory: drop the expired ones
			for k, v := range s.fails {
				if !now.Before(v.until) && v.n < 5 {
					delete(s.fails, k)
				}
			}
		}
		s.fails[source] = &failure{}
	}
	f = s.fails[source]
	f.n++
	switch {
	case f.n >= 10:
		f.until = s.Now().Add(900 * time.Second)
	case f.n >= 5:
		f.until = s.Now().Add(60 * time.Second)
	}
	return ErrWrong
}

// ClearBackoff forgets every source's wrong tries and the machine-wide count (the owner at
// the machine, or a new PIN set).
func (s *Store) ClearBackoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails = map[string]*failure{}
	s.global = failure{}
	s.saveTries()
}

// WaitError: too many wrong PINs from this source; try again after Wait.
type WaitError struct{ Wait time.Duration }

func (e *WaitError) Error() string {
	return "too many wrong PINs: wait " + e.Wait.Round(time.Second).String() + " (or clear it at the machine)"
}
