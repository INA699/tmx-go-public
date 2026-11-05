package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mkdirSecret never sets security information on an existing object. The only
// directory DACL we supply is part of an exclusive CreateDirectory operation
// (an existing secrets folder is re-protected separately: reprotectDir).
// Inspect every ancestor through a no-follow handle as well as by pathname.
// FILE_READ_ATTRIBUTES-only handles do not prevent concurrent ancestor renames:
// path-based creation still has a residual check/use race. Exclusive creation
// ensures this code never rewrites an existing object's DACL. Closing that race
// requires creation relative to a parent handle.
func mkdirSecret(dir string) (err error) {
	if err := testModePath(dir); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w; set TMXG_HOME to a plain local folder without junctions or symlinks", err)
			slog.Warn("config: refused secrets directory", "dir", dir, "err", err)
		}
	}()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	if len(volume) != 2 || volume[1] != ':' || strings.ContainsAny(abs[len(volume):], ":") {
		return fmt.Errorf("unsafe secrets path: local drive path required")
	}
	parts := strings.Split(strings.TrimPrefix(abs[len(volume):], `\`), `\`)
	for _, part := range parts {
		if part == "" || strings.TrimRight(part, " .") != part {
			return fmt.Errorf("unsafe secrets path component %q", part)
		}
	}
	policy, err := secretDirectoryPolicy()
	if err != nil {
		return err
	}
	if err := policy.check(abs); err != nil {
		return err
	}
	var handles []windows.Handle
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}()
	current := volume + `\`
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			current = filepath.Join(current, parts[i])
		}
		fi, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && i >= 0 {
			if err := testModePath(current); err != nil {
				return err
			}
			name, e := windows.UTF16PtrFromString(current)
			if e != nil {
				return e
			}
			var sa *windows.SecurityAttributes
			if i == len(parts)-1 {
				sd, _, e := secretSD(true)
				if e != nil {
					return e
				}
				sa = securityAttributes(sd)
			}
			// A competing creator does not authorize changing its DACL. Inspect
			// the object it supplied using the same no-follow checks below.
			if e := windows.CreateDirectory(name, sa); e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
				return &os.PathError{Op: "mkdir", Path: current, Err: e}
			}
			fi, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe secrets ancestor: %s", current)
		}
		name, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(name)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("reparse point in secrets ancestry: %s", current)
		}
		h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return &os.PathError{Op: "lock secrets ancestor", Path: current, Err: err}
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fmt.Errorf("unsafe secrets ancestor changed during check: %s", current)
		}
		// Resolve short (8.3) names by handle, never by following a reparse
		// point. Check the eventual leaf before creating the next component.
		buf := make([]uint16, 32768)
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return err
		}
		if n >= uint32(len(buf)) {
			return errors.New("secrets path too long")
		}
		canonical := strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
		target := filepath.Join(append([]string{canonical}, parts[i+1:]...)...)
		if err := policy.check(target); err != nil {
			return err
		}
	}
	return nil
}

type secretDirPolicy struct {
	profile  string
	profiles string
	system   []string
}

func secretDirectoryPolicy() (secretDirPolicy, error) {
	var p secretDirPolicy
	var err error
	p.profile, err = windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		return p, err
	}
	p.profiles, err = windows.KnownFolderPath(windows.FOLDERID_UserProfiles, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		return p, err
	}
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Windows, windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_ProgramData} {
		path, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY)
		if err != nil {
			return p, err
		}
		p.system = append(p.system, path)
	}
	return p, nil
}

func (p secretDirPolicy) check(path string) error {
	path = filepath.Clean(path)
	if filepath.Dir(path) == path || strings.EqualFold(path, filepath.Clean(p.profile)) ||
		strings.EqualFold(path, filepath.Clean(p.profiles)) || strings.EqualFold(filepath.Dir(path), filepath.Clean(p.profiles)) {
		return fmt.Errorf("refusing secrets directory at drive or profile root: %s", path)
	}
	for _, system := range p.system {
		system = filepath.Clean(system)
		if strings.EqualFold(path, system) || strings.HasPrefix(strings.ToLower(path), strings.ToLower(system)+`\`) {
			return fmt.Errorf("refusing secrets directory in system folder: %s", path)
		}
	}
	return nil
}

// reprotectDir gives an existing secrets folder the DACL a new one gets (only the current user
// and SYSTEM, protected, inherited by what is in it), unless it has it already: folders made by
// older versions, or before this one, kept whatever they inherited (often Users or Everyone).
// The folder is tmx's own and the current user keeps full control, so this only takes access
// away from others; files in it that just inherit their ACL take the new entries too.
//
// It must never rewrite another object's DACL through a planted link (#75), so it only runs
// after mkdirSecret has checked dir (a plain local folder, no junction or symlink anywhere up to
// it, not a system or profile folder), and it works on one handle opened without following a
// reparse point and without FILE_SHARE_DELETE (the folder cannot be renamed or replaced while it
// is open): that handle must be a directory, not a reparse point, at exactly the path checked (a
// junction swapped in above it since would show in its final path), owned by the current user
// (ownedHere).
// Nothing inside may be a reparse point, since the new entries are passed down to what is
// inside. Otherwise the folder is left as it is, with an error the caller logs. A link planted
// inside between that walk and the change remains possible, only for someone who can already
// write into the folder.
func reprotectDir(dir string) error {
	if err := testModePath(dir); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	sd, user, err := secretSD(true)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "open secrets folder", Path: abs, Err: err}
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("unsafe secrets folder: %s", abs)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return err
	}
	if n >= uint32(len(buf)) {
		return errors.New("secrets path too long")
	}
	final := strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
	// abs in long form (no 8.3 names; a temp folder often has one), to compare with final.
	if n, err = windows.GetLongPathName(name, &buf[0], uint32(len(buf))); err != nil || n >= uint32(len(buf)) {
		return fmt.Errorf("secrets folder %s: no long path name (%v)", abs, err)
	}
	if long := windows.UTF16ToString(buf[:n]); !strings.EqualFold(filepath.Clean(final), filepath.Clean(long)) {
		return fmt.Errorf("secrets folder %s is %s by its handle; left as it is", long, final)
	}
	policy, err := secretDirectoryPolicy()
	if err != nil {
		return err
	}
	if err := policy.check(final); err != nil {
		return err
	}
	cur, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if isPrivate(cur, user) {
		return nil
	}
	if !ownedHere(cur, user) {
		return fmt.Errorf("secrets folder %s is not owned by the current user; left as it is", abs)
	}
	if err := noReparsePointInside(abs); err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// ownedHere reports whether sd's owner is user, or the owner this process gives what it creates
// (TokenOwner: for an elevated administrator often the Administrators group), so a folder this
// user made, elevated or not. Never another user's.
func ownedHere(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) bool {
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false
	}
	if owner.Equals(user) {
		return true
	}
	t := windows.GetCurrentProcessToken()
	n := uint32(128)
	for range 4 {
		b := make([]byte, n)
		err := windows.GetTokenInformation(t, windows.TokenOwner, &b[0], uint32(len(b)), &n)
		if err == nil {
			own := (*struct{ Owner *windows.SID })(unsafe.Pointer(&b[0])).Owner
			return own != nil && owner.Equals(own)
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return false
		}
	}
	return false
}

// noReparsePointInside walks dir without following links and fails on the first reparse point
// (junction, symlink or other) below it.
func noReparsePointInside(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(name)
		if err != nil {
			return &os.PathError{Op: "inspect", Path: path, Err: err}
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("link inside the secrets folder (%s); folder left as it is", path)
		}
		return nil
	})
}
