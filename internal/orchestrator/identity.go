package orchestrator

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
)

var ErrInvalidCommitPreparationIdentity = errors.New("некорректная идентичность подготовки коммитов")

type CommitPreparationIdentity struct {
	WorkingTreeRoot  string
	PlanningHomeRoot string
	ChangeName       string
	ServerID         string
}

func NewCommitPreparationChangeKey(identity CommitPreparationIdentity) (ChangeKey, error) {
	workingTreeRoot, err := canonicalIdentityRoot("корень рабочего дерева", identity.WorkingTreeRoot)
	if err != nil {
		return ChangeKey{}, err
	}
	planningHomeRoot, err := canonicalIdentityRoot("planning home", identity.PlanningHomeRoot)
	if err != nil {
		return ChangeKey{}, err
	}
	if err := validateIdentifier("имя change", identity.ChangeName); err != nil {
		return ChangeKey{}, fmt.Errorf("%w: %v", ErrInvalidCommitPreparationIdentity, err)
	}
	if err := validateIdentifier("server ID", identity.ServerID); err != nil {
		return ChangeKey{}, fmt.Errorf("%w: %v", ErrInvalidCommitPreparationIdentity, err)
	}

	digest := sha256.New()
	for _, value := range []string{
		workingTreeRoot,
		planningHomeRoot,
		identity.ChangeName,
		identity.ServerID,
	} {
		writeIdentityValue(digest, value)
	}
	return newChangeKey(
		"change-v1-"+hex.EncodeToString(digest.Sum(nil)),
		identity.ChangeName,
	)
}

func canonicalIdentityRoot(name, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: %s не задан", ErrInvalidCommitPreparationIdentity, name)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%w: определить абсолютный %s: %v", ErrInvalidCommitPreparationIdentity, name, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: канонизировать %s: %v", ErrInvalidCommitPreparationIdentity, name, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: прочитать %s: %v", ErrInvalidCommitPreparationIdentity, name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s не является каталогом", ErrInvalidCommitPreparationIdentity, name)
	}
	return filepath.Clean(canonical), nil
}

func writeIdentityValue(destination hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write([]byte(value))
}
