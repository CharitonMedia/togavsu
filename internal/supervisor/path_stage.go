package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// tempTokenPrefixBytes is the on-disk ownership prefix used when the
// filesystem rejects user xattrs: "TGVTMP1\n" + 32 hex + "\n" = 41.
const tempTokenPrefixBytes = 41

// PathIdentity is one durable change to a path upload's staged names.
// Empty fields leave the receipt's previous values in place. Release clears
// the source temp, its anchor, and the clean publication file together.
type PathIdentity struct {
	Temp            string
	Inode           uint64
	Token           string
	Link            string
	Clean           string
	CleanInode      uint64
	TempQuarantine  string
	LinkQuarantine  string
	CleanQuarantine string
	Hold            string
	Conflict        bool
	Release         bool
}

func applyPathIdentity(obj *OwnedObject, id PathIdentity) {
	if obj == nil {
		return
	}
	if id.Release {
		obj.releaseTemp = true
		obj.releaseClean = true
		obj.Temp = ""
		obj.TempInode = 0
		obj.TempToken = ""
		obj.TempLink = ""
		obj.TempQuarantine = ""
		obj.LinkQuarantine = ""
		obj.Clean = ""
		obj.CleanInode = 0
		obj.CleanQuarantine = ""
		obj.TempHold = ""
		obj.TempConflict = false
		return
	}
	if id.Temp != "" {
		obj.Temp = id.Temp
		obj.TempInode = id.Inode
		obj.TempToken = id.Token
		obj.TempLink = id.Link
	}
	if id.Clean != "" {
		obj.Clean = id.Clean
		obj.CleanInode = id.CleanInode
	}
	if id.TempQuarantine != "" {
		obj.TempQuarantine = id.TempQuarantine
	}
	if id.LinkQuarantine != "" {
		obj.LinkQuarantine = id.LinkQuarantine
	}
	if id.CleanQuarantine != "" {
		obj.CleanQuarantine = id.CleanQuarantine
	}
	if id.Hold != "" {
		obj.TempHold = id.Hold
	}
	if id.Conflict {
		obj.TempConflict = true
	}
}

func (p *PathDestination) journal(ctx context.Context, id PathIdentity) error {
	own := pathTempOwner(ctx)
	if own == nil {
		return nil
	}
	return own(id)
}

func quarantineName(name string) (string, error) {
	hexPart, ok := tempHex(name)
	if !ok {
		return "", errTempIdentity
	}
	suffix := ".qrt"
	if len(name) > 4 && name[len(name)-4:] == ".own" {
		suffix = ".oqr"
	}
	return ".togavsu-" + hexPart + suffix, nil
}

func copyCleanPayload(p *PathDestination, src io.Reader, dst *os.File) error {
	buf := make([]byte, 32*1024)
	stalled := false
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			if !stalled && p != nil && p.stall != nil {
				stalled = true
				p.stall("clean-copy")
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func dirEntryExists(dir *os.File, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == syscall.ENOENT {
		return false, nil
	}
	if err == syscall.ELOOP || err == syscall.EISDIR {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	syscall.Close(fd)
	return true, nil
}

// disposeRecorded journals the quarantine name, renames that one directory
// entry aside, and deletes it only when the inode is the owned one. A
// different inode is renamed back. RENAME_NOREPLACE refuses to overwrite a
// name that was created after the move. The foreign file then stays at the
// hold and the caller keeps the anchor.
func (p *PathDestination) disposeRecorded(ctx context.Context, dir *os.File, name, hold string, inode uint64, role string, needToken bool, token string) error {
	if name == "" || inode == 0 {
		return nil
	}
	var err error
	if hold == "" {
		hold, err = quarantineName(name)
		if err != nil {
			return err
		}
	}
	id := PathIdentity{}
	switch role {
	case "temp":
		id.TempQuarantine = hold
	case "link":
		id.LinkQuarantine = hold
	case "clean":
		id.CleanQuarantine = hold
	default:
		return errTempIdentity
	}
	if err := p.journal(ctx, id); err != nil {
		return err
	}
	held, err := dirEntryExists(dir, hold)
	if err != nil {
		return err
	}
	if !held {
		if p.stall != nil {
			p.stall("quarantine")
		}
		present, err := dirEntryExists(dir, name)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if err := renameNoReplaceAt(int(dir.Fd()), name, int(dir.Fd()), hold); err != nil {
			if err == syscall.ENOENT {
				return nil
			}
			if err == syscall.EEXIST {
				return errTempIdentity
			}
			return err
		}
	}
	if p.stall != nil {
		p.stall("quarantine-held")
	}
	return p.finishHold(dir, name, hold, inode, needToken, token)
}

func (p *PathDestination) finishHold(dir *os.File, original, hold string, inode uint64, needToken bool, token string) error {
	fd, err := syscall.Openat(int(dir.Fd()), hold, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == syscall.ELOOP || err == syscall.EISDIR {
		return p.restoreForeign(dir, original, hold)
	}
	if err == syscall.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var info syscall.Stat_t
	if err := syscall.Fstat(fd, &info); err != nil {
		return err
	}
	owned := info.Mode&syscall.S_IFMT == syscall.S_IFREG && info.Ino == inode
	if owned && needToken && !tokenOnFile(fd, token) {
		owned = false
	}
	if !owned {
		return p.restoreForeign(dir, original, hold)
	}
	if err := syscall.Unlinkat(int(dir.Fd()), hold); err != nil && err != syscall.ENOENT {
		return err
	}
	return nil
}

func (p *PathDestination) restoreForeign(dir *os.File, original, hold string) error {
	err := renameNoReplaceAt(int(dir.Fd()), hold, int(dir.Fd()), original)
	if err == syscall.EEXIST || err == syscall.ENOENT {
		return tempDisplacedError{hold: hold}
	}
	if err != nil {
		return err
	}
	return errTempIdentity
}

// keepAnchor records a foreign disposition and returns it to the caller
// without unlinking the private anchor. The owned inode stays reachable
// until a later pass sees temp_conflict.
func (p *PathDestination) keepAnchor(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var displaced tempDisplacedError
	if !errors.As(err, &displaced) && !errors.Is(err, errTempIdentity) {
		return err
	}
	id := PathIdentity{Conflict: true}
	if displaced.hold != "" {
		id.Hold = displaced.hold
	}
	if jerr := p.journal(ctx, id); jerr != nil {
		return jerr
	}
	return err
}

func (p *PathDestination) removeOwnedTemp(ctx context.Context, obj OwnedObject) error {
	dir, err := p.openNamespace(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer dir.Close()
	if p.stall != nil {
		p.stall("cleanup")
	}
	if p.stall != nil {
		p.stall("dispose")
	}
	fd, err := p.openOwned(dir, obj)
	if err != nil {
		if errors.Is(err, errTempBusy) {
			return err
		}
		if errors.Is(err, errTempIdentity) {
			if !obj.TempConflict {
				if jerr := p.journal(ctx, PathIdentity{Conflict: true}); jerr != nil {
					return jerr
				}
			}
			return err
		}
		if os.IsNotExist(err) {
			return p.acknowledgeCleared(dir, ownedStageNames(obj))
		}
		return err
	}
	defer syscall.Close(fd)
	if obj.TempConflict {
		return p.retireConflict(ctx, dir, obj)
	}
	if obj.Temp != "" {
		if err := p.disposeRecorded(ctx, dir, obj.Temp, obj.TempQuarantine, obj.TempInode, "temp", true, obj.TempToken); err != nil {
			return p.keepAnchor(ctx, err)
		}
	}
	if obj.Clean != "" {
		if err := p.disposeRecorded(ctx, dir, obj.Clean, obj.CleanQuarantine, obj.CleanInode, "clean", false, ""); err != nil {
			return p.keepAnchor(ctx, err)
		}
	}
	if obj.TempLink != "" {
		if err := p.disposeRecorded(ctx, dir, obj.TempLink, obj.LinkQuarantine, obj.TempInode, "link", true, obj.TempToken); err != nil {
			return err
		}
	}
	return p.acknowledgeCleared(dir, ownedStageNames(obj))
}

// retireConflict removes the owned inode through the anchor after a foreign
// name has already been recorded. It does not rename the foreign temp and it
// does not require a parked hold to disappear.
func (p *PathDestination) retireConflict(ctx context.Context, dir *os.File, obj OwnedObject) error {
	if obj.TempLink != "" {
		if err := p.disposeRecorded(ctx, dir, obj.TempLink, obj.LinkQuarantine, obj.TempInode, "link", true, obj.TempToken); err != nil {
			return err
		}
	}
	if obj.Clean != "" {
		if err := p.disposeRecorded(ctx, dir, obj.Clean, obj.CleanQuarantine, obj.CleanInode, "clean", false, ""); err != nil {
			return p.keepAnchor(ctx, err)
		}
	}
	names := []string{obj.TempLink, obj.LinkQuarantine, obj.Clean, obj.CleanQuarantine}
	if obj.TempHold == "" {
		names = append(names, obj.TempQuarantine)
	}
	if err := p.acknowledgeCleared(dir, names); err != nil {
		return err
	}
	if obj.TempHold != "" {
		return tempDisplacedError{hold: obj.TempHold}
	}
	return nil
}

func ownedStageNames(obj OwnedObject) []string {
	return []string{obj.Temp, obj.TempQuarantine, obj.TempLink, obj.LinkQuarantine, obj.Clean, obj.CleanQuarantine}
}

func (obj OwnedObject) hasStaging() bool {
	return obj.Temp != "" || obj.TempLink != "" || obj.TempQuarantine != "" || obj.LinkQuarantine != "" ||
		obj.Clean != "" || obj.CleanQuarantine != "" || obj.TempHold != "" || obj.TempConflict
}

// receiptOpen is a destination row that still blocks retention and removal.
// cleanup_verified alone does not: a confirmed copy keeps that flag and
// must still be eligible for retention.
func receiptOpen(obj OwnedObject) bool {
	if obj.hasStaging() {
		return true
	}
	switch obj.State {
	case receiptPending, receiptUncertain, receiptRejected, receiptObserved, receiptAborted:
		return true
	default:
		return false
	}
}

type ownedOpen struct {
	name      string
	inode     uint64
	needToken bool
}

// openOwned opens the recorded inode. A symlink or directory is not opened.
// The clean publication file has no token; its journaled inode is the check.
func (p *PathDestination) openOwned(dir *os.File, obj OwnedObject) (int, error) {
	candidates := []ownedOpen{
		{obj.Temp, obj.TempInode, true},
		{obj.TempQuarantine, obj.TempInode, true},
		{obj.TempLink, obj.TempInode, true},
		{obj.LinkQuarantine, obj.TempInode, true},
		{obj.Clean, obj.CleanInode, false},
		{obj.CleanQuarantine, obj.CleanInode, false},
	}
	missing := 0
	checked := 0
	for _, cand := range candidates {
		if cand.name == "" || cand.inode == 0 {
			continue
		}
		checked++
		fd, err := syscall.Openat(int(dir.Fd()), cand.name, syscall.O_RDWR|syscall.O_NOFOLLOW, 0)
		if err == syscall.ENOENT {
			missing++
			continue
		}
		if err == syscall.ELOOP || err == syscall.EISDIR {
			continue
		}
		if err != nil {
			return -1, err
		}
		var info syscall.Stat_t
		if err := syscall.Fstat(fd, &info); err != nil {
			syscall.Close(fd)
			return -1, err
		}
		matched := info.Mode&syscall.S_IFMT == syscall.S_IFREG && info.Ino == cand.inode
		if matched && cand.needToken && !tokenOnFile(fd, obj.TempToken) {
			matched = false
		}
		if !matched {
			syscall.Close(fd)
			continue
		}
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			syscall.Close(fd)
			return -1, errTempBusy
		}
		return fd, nil
	}
	if checked > 0 && missing == checked {
		return -1, syscall.ENOENT
	}
	return -1, errTempIdentity
}

// acknowledgeCleared syncs the directory once, then requires every named
// entry to be absent. One sync failure or a remaining name keeps the receipt.
func (p *PathDestination) acknowledgeCleared(dir *os.File, names []string) error {
	if err := p.syncDirectory(dir, "delete"); err != nil {
		return fmt.Errorf("%w: directory sync after temp delete: %v", ErrRemoteUncertain, err)
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		present, err := dirEntryExists(dir, name)
		if err != nil {
			return err
		}
		if present {
			return errTempIdentity
		}
	}
	return nil
}

func (p *PathDestination) retirePublishedStaging(ctx context.Context, dir *os.File, header bool, tmpName, linkName string, inode uint64, token string) error {
	var names []string
	if header && tmpName != "" {
		if err := p.disposeRecorded(ctx, dir, tmpName, "", inode, "temp", true, token); err != nil {
			return fmt.Errorf("%w: source temp: %v", ErrRemoteUncertain, err)
		}
		names = append(names, tmpName)
	}
	if linkName != "" {
		if err := p.disposeRecorded(ctx, dir, linkName, "", inode, "link", true, token); err != nil {
			return fmt.Errorf("%w: anchor: %v", ErrRemoteUncertain, err)
		}
		names = append(names, linkName)
	}
	if len(names) == 0 {
		return nil
	}
	return p.acknowledgeCleared(dir, names)
}

func (p *PathDestination) publishedInode(name string) (uint64, error) {
	dir, err := p.openNamespace(false)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd)
	var info syscall.Stat_t
	if err := syscall.Fstat(fd, &info); err != nil {
		return 0, err
	}
	if info.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return 0, errTempIdentity
	}
	return info.Ino, nil
}
