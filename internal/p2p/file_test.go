// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package p2p

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pairNodes starts two hosts and pairs them with a DM invite (a invites, b joins).
func pairNodes(t *testing.T) (a, b *testNode) {
	t.Helper()
	a = startNode(t, "alice", nil, "")
	b = startNode(t, "bob", nil, "")
	inv, err := a.CreateInvite(InviteOptions{Kind: InviteDM})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Join(ctxT(t), inv.Link); err != nil {
		t.Fatal(err)
	}
	recv(t, a.Paired(), "alice Paired")
	return a, b
}

// trackedReader counts Close calls; block (if set) holds the first Read until closed.
type trackedReader struct {
	io.Reader
	closed *atomic.Int32
	block  chan struct{}
}

func (r trackedReader) Read(p []byte) (int, error) {
	if r.block != nil {
		<-r.block
	}
	return r.Reader.Read(p)
}

func (r trackedReader) Close() error { r.closed.Add(1); return nil }

type fileStore struct {
	mu     sync.Mutex
	files  map[string][]byte // file ID → content
	asked  []FileRequest
	closed atomic.Int32
	block  chan struct{}
	lie    int64 // >0: announce this size, whatever the content
}

func (fs *fileStore) source(req FileRequest) (io.ReadCloser, int64, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.asked = append(fs.asked, req)
	b, ok := fs.files[req.FileID]
	if !ok || req.ChatID != "chat1" {
		return nil, 0, errors.New("no such file in /secret/path")
	}
	size := int64(len(b))
	if fs.lie > 0 {
		size = fs.lie
	}
	return trackedReader{Reader: bytes.NewReader(b), closed: &fs.closed, block: fs.block}, size, nil
}

func (fs *fileStore) lastAsked() FileRequest {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.asked[len(fs.asked)-1]
}

func TestFileFetch(t *testing.T) {
	alice, bob := pairNodes(t)
	big := make([]byte, 3<<20+12345)
	_, _ = rand.Read(big)
	fs := &fileStore{files: map[string][]byte{"big": big, "empty": {}}}
	alice.ServeFiles(fs.source)

	var out bytes.Buffer
	start := time.Now()
	n, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "big", &out, 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes in %s", n, time.Since(start).Round(time.Millisecond))
	if n != int64(len(big)) || sha256.Sum256(out.Bytes()) != sha256.Sum256(big) {
		t.Fatalf("got %d bytes (want %d), sha256 matches: %v", n, len(big), sha256.Sum256(out.Bytes()) == sha256.Sum256(big))
	}
	if req := fs.lastAsked(); req.PeerID != bob.PeerID() || req.ChatID != "chat1" || req.FileID != "big" {
		t.Fatalf("source asked %+v, want bob's peer ID %s", req, bob.PeerID())
	}
	waitFor(t, "source reader closed", func() bool { return fs.closed.Load() == 1 })

	// An empty file is a valid file.
	out.Reset()
	if n, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "empty", &out, 10); err != nil || n != 0 {
		t.Fatalf("empty file: %d, %v", n, err)
	}

	// Larger than the caller's limit: ErrTooBig, nothing written.
	out.Reset()
	if n, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "big", &out, int64(len(big))-1); !errors.Is(err, ErrTooBig) || n != 0 || out.Len() != 0 {
		t.Fatalf("too big: %d, %v, wrote %d", n, err, out.Len())
	}

	// The source refuses (unknown file, wrong chat): ErrNoFile, and its reason stays local.
	for _, c := range [][2]string{{"chat1", "nope"}, {"chat2", "big"}} {
		_, err := bob.Fetch(ctxT(t), alice.PeerID(), c[0], c[1], &out, 1<<20)
		if !errors.Is(err, ErrNoFile) || bytes.Contains([]byte(err.Error()), []byte("/secret")) {
			t.Fatalf("refused %v: %v", c, err)
		}
	}
	// bob serves nothing: ErrNoFile the other way round.
	if _, err := alice.Fetch(ctxT(t), bob.PeerID(), "chat1", "big", &out, 1<<20); !errors.Is(err, ErrNoFile) {
		t.Fatalf("no source: %v", err)
	}
	// Bad IDs are refused locally.
	if _, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "", &out, 1); err == nil || errors.Is(err, ErrNoFile) {
		t.Fatalf("empty file ID: %v", err)
	}

	// The source announces more than it has: the server resets the stream, the transfer
	// breaks off (retry later). The reset may also discard the header already sent.
	fs.mu.Lock()
	fs.lie = 1000
	fs.files["short"] = []byte("only ten b")
	fs.mu.Unlock()
	out.Reset()
	if n, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "short", &out, 1<<20); !errors.Is(err, ErrUnreachable) || n > 10 {
		t.Fatalf("short source: %d, %v", n, err)
	}
	fs.mu.Lock()
	fs.lie = 0
	fs.mu.Unlock()

	// Stop serving: ErrNoFile.
	alice.ServeFiles(nil)
	if _, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "big", &out, 25<<20); !errors.Is(err, ErrNoFile) {
		t.Fatalf("after ServeFiles(nil): %v", err)
	}
}

// TestFileBusy: at most filesPerPeer streams per peer; one more is refused at once as
// retryable (ErrUnreachable), and works again once the others finish.
func TestFileBusy(t *testing.T) {
	alice, bob := pairNodes(t)
	fs := &fileStore{files: map[string][]byte{"f": []byte("hello")}, block: make(chan struct{})}
	alice.ServeFiles(fs.source)
	var wg sync.WaitGroup
	errs := make(chan error, filesPerPeer)
	for range filesPerPeer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out bytes.Buffer
			_, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", &out, 100)
			if err == nil && out.String() != "hello" {
				err = errors.New("wrong content " + out.String())
			}
			errs <- err
		}()
	}
	waitFor(t, "both streams being served", func() bool {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		return len(fs.asked) == filesPerPeer
	})
	start := time.Now()
	if _, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", io.Discard, 100); !errors.Is(err, ErrUnreachable) || time.Since(start) > 5*time.Second {
		t.Fatalf("third concurrent fetch: %v after %s", err, time.Since(start))
	}
	close(fs.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", io.Discard, 100); err != nil {
		t.Fatalf("after the others finished: %v", err)
	}
}

// TestFileUnpaired: a peer that alice never paired with (nor trusted) is refused, even
// when it trusts alice on its own side; without that it is refused locally. With no invite
// open, alice's connection gater refuses it before /tmx/file runs (ErrUnreachable to it);
// while one is open, strangers may connect and handleFile itself refuses (ErrNoFile).
func TestFileUnpaired(t *testing.T) {
	alice, bob := pairNodes(t)
	carol := startNode(t, "carol", nil, "")
	fs := &fileStore{files: map[string][]byte{"f": []byte("secret")}}
	alice.ServeFiles(fs.source)
	if _, err := carol.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", io.Discard, 100); !errors.Is(err, ErrNoFile) {
		t.Fatalf("carol, no contact: %v", err)
	}
	if err := carol.Trust(alice.PeerID(), "alice", bob.PeerAddrs(alice.PeerID())); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := carol.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", &out, 100); !errors.Is(err, ErrUnreachable) || out.Len() != 0 {
		t.Fatalf("carol, not trusted by alice, refused by the gater: %v (%q)", err, out.String())
	}
	if _, err := alice.CreateInvite(InviteOptions{Kind: InviteDM}); err != nil {
		t.Fatal(err)
	}
	if _, err := carol.Fetch(ctxT(t), alice.PeerID(), "chat1", "f", &out, 100); !errors.Is(err, ErrNoFile) || out.Len() != 0 {
		t.Fatalf("carol, not trusted by alice: %v (%q)", err, out.String())
	}
	fs.mu.Lock()
	asked := len(fs.asked)
	fs.mu.Unlock()
	if asked != 0 {
		t.Fatalf("the source was asked for an unpaired peer: %d", asked)
	}
}

// TestFileUnreachable: the peer is gone → ErrUnreachable (retry later), quickly.
func TestFileUnreachable(t *testing.T) {
	alice, bob := pairNodes(t)
	bob.ServeFiles((&fileStore{files: map[string][]byte{"f": []byte("x")}}).source)
	bobID := bob.PeerID()
	bob.Close()
	waitPeerEvent(t, alice, bobID, false)
	start := time.Now()
	if _, err := alice.Fetch(ctxT(t), bobID, "chat1", "f", io.Discard, 100); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("peer gone: %v", err)
	}
	t.Logf("failed after %s", time.Since(start).Round(time.Millisecond))
}

func TestRelayedOK(t *testing.T) {
	for _, c := range []struct {
		limited bool
		size    int64
		ok      bool
	}{{false, 25 << 20, true}, {true, 0, true}, {true, maxRelayedFile, true}, {true, maxRelayedFile + 1, false}} {
		if relayedOK(c.limited, c.size) != c.ok {
			t.Errorf("relayedOK(%v, %d) != %v", c.limited, c.size, c.ok)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestFileFetchTCP: the same over TCP + yamux (stream close and reset behave differently
// from QUIC): a large file arrives intact and a refusal sent before reading the request
// still reaches the requester.
func TestFileFetchTCP(t *testing.T) {
	start := func(name string) *Host {
		key, _ := testKey(t)
		h, err := New(Options{Key: key, Name: name, Version: "test", AllowLoopback: true,
			ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}, RetryInterval: 400 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		assertLoopbackMultiaddrs(t, h.h.Network().ListenAddresses())
		assertLoopbackMultiaddrs(t, h.h.Addrs())
		t.Cleanup(func() { h.Close() })
		return h
	}
	alice, bob, carol := start("alice"), start("bob"), start("carol")
	inv, _ := alice.CreateInvite(InviteOptions{Kind: InviteDM})
	if _, err := bob.Join(ctxT(t), inv.Link); err != nil {
		t.Fatal(err)
	}
	recv(t, alice.Paired(), "alice Paired")
	big := make([]byte, 3<<20)
	_, _ = rand.Read(big)
	alice.ServeFiles((&fileStore{files: map[string][]byte{"big": big}}).source)
	var out bytes.Buffer
	if n, err := bob.Fetch(ctxT(t), alice.PeerID(), "chat1", "big", &out, 25<<20); err != nil || n != int64(len(big)) ||
		sha256.Sum256(out.Bytes()) != sha256.Sum256(big) {
		t.Fatalf("over TCP: %d, %v", n, err)
	}
	for _, c := range alice.h.Network().ConnsToPeer(bob.h.ID()) {
		if c.ConnState().Transport != "tcp" {
			t.Fatalf("transport %q", c.ConnState().Transport)
		}
	}
	if err := carol.Trust(alice.PeerID(), "alice", bob.PeerAddrs(alice.PeerID())); err != nil {
		t.Fatal(err)
	}
	// An open invite lets carol past alice's connection gater (gater.go), so the refusal
	// tested here is handleFile's own.
	if _, err := alice.CreateInvite(InviteOptions{Kind: InviteDM}); err != nil {
		t.Fatal(err)
	}
	if _, err := carol.Fetch(ctxT(t), alice.PeerID(), "chat1", "big", io.Discard, 25<<20); !errors.Is(err, ErrNoFile) {
		t.Fatalf("unpaired over TCP: %v", err)
	}
}
