//go:build linux

package ownership

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	extFilesystemMagic      filesystemMagic = 0xef53
	xfsFilesystemMagic      filesystemMagic = 0x58465342
	btrfsFilesystemMagic    filesystemMagic = 0x9123683e
	f2fsFilesystemMagic     filesystemMagic = 0xf2f52010
	nilfsFilesystemMagic    filesystemMagic = 0x3434
	bcachefsFilesystemMagic filesystemMagic = 0xca451a4e
	zfsFilesystemMagic      filesystemMagic = 0x2fc12fc1
	tmpfsFilesystemMagic    filesystemMagic = 0x01021994
	overlayFilesystemMagic  filesystemMagic = 0x794c7630
	nfsFilesystemMagic      filesystemMagic = 0x6969
	cifsFilesystemMagic     filesystemMagic = 0xff534d42
	smb2FilesystemMagic     filesystemMagic = 0xfe534d42
	v9fsFilesystemMagic     filesystemMagic = 0x01021997
	cephFilesystemMagic     filesystemMagic = 0x00c36400
	fuseFilesystemMagic     filesystemMagic = 0x65735546
)

type filesystemMagic uint32

func CheckLocalEnvironment(workingRoot, changeRoot string) (LocalEnvironment, error) {
	canonicalWorkingRoot, err := validateLocalRoot("рабочий корень", workingRoot)
	if err != nil {
		return LocalEnvironment{}, err
	}
	canonicalChangeRoot, err := validateLocalRoot("корень change", changeRoot)
	if err != nil {
		return LocalEnvironment{}, err
	}

	return LocalEnvironment{
		workingRoot: canonicalWorkingRoot,
		changeRoot:  canonicalChangeRoot,
	}, nil
}

func validateLocalRoot(name, root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: %s не задан", ErrInvalidRoot, name)
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: определить абсолютный путь %s: %v", ErrInvalidRoot, name, err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("%w: разрешить %s %q: %v", ErrInvalidRoot, name, absoluteRoot, err)
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("%w: прочитать %s %q: %v", ErrInvalidRoot, name, canonicalRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s %q не является каталогом", ErrInvalidRoot, name, canonicalRoot)
	}

	var stats syscall.Statfs_t
	if err := syscall.Statfs(canonicalRoot, &stats); err != nil {
		return "", fmt.Errorf("%w: %s %q: %v", ErrFilesystemInspection, name, canonicalRoot, err)
	}
	if err := validateFilesystem(name, canonicalRoot, filesystemMagic(stats.Type)); err != nil {
		return "", err
	}
	return canonicalRoot, nil
}

func validateFilesystem(rootName, root string, magic filesystemMagic) error {
	name, supported := filesystemName(magic)
	if supported {
		return nil
	}
	if name == "" {
		name = "неизвестная файловая система"
	}
	return fmt.Errorf(
		"%w: %s %q использует %s (0x%x)",
		ErrUnsupportedFilesystem,
		rootName,
		root,
		name,
		uint32(magic),
	)
}

func filesystemName(magic filesystemMagic) (string, bool) {
	switch magic {
	case extFilesystemMagic:
		return "ext2/ext3/ext4", true
	case xfsFilesystemMagic:
		return "xfs", true
	case btrfsFilesystemMagic:
		return "btrfs", true
	case f2fsFilesystemMagic:
		return "f2fs", true
	case nilfsFilesystemMagic:
		return "nilfs", true
	case bcachefsFilesystemMagic:
		return "bcachefs", true
	case zfsFilesystemMagic:
		return "zfs", true
	case tmpfsFilesystemMagic:
		return "tmpfs", true
	case overlayFilesystemMagic:
		return "overlayfs", true
	case nfsFilesystemMagic:
		return "nfs", false
	case cifsFilesystemMagic:
		return "cifs", false
	case smb2FilesystemMagic:
		return "smb2", false
	case v9fsFilesystemMagic:
		return "9p", false
	case cephFilesystemMagic:
		return "ceph", false
	case fuseFilesystemMagic:
		return "fuse", false
	default:
		return "", false
	}
}
