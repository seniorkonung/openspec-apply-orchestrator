package prompts

import (
	"strings"
	"testing"
)

func TestВстроенноеПоручениеОграничиваетРаботуПодготовкойКоммитов(t *testing.T) {
	prompt := CommitPreparation()
	text := prompt.Text()
	if strings.TrimSpace(text) == "" {
		t.Fatal("встроенное поручение пусто")
	}
	normalized := strings.Join(strings.Fields(text), " ")

	requiredStatements := []string{
		"Commit all current repository changes",
		"Follow all repository instructions",
		"Do not start or continue implementation",
		"Never discard, reset, overwrite, or stash any current change",
		"Never resolve conflicts or ambiguous ownership by guessing",
		"Do not modify code or documentation merely to make a check pass",
		"Do not bypass hooks or verification",
		"Do not push commits",
		"Do not amend, rebase, reset, or otherwise rewrite pre-existing history",
		"Do not force-add ignored files",
		"explain the blocker in this Paseo session and wait for the user",
		"No special structured response is required",
	}
	for _, statement := range requiredStatements {
		if !strings.Contains(normalized, statement) {
			t.Errorf("поручение не содержит обязательное ограничение %q", statement)
		}
	}
}

func TestВстроенноеПоручениеТребуетПроверокИЧистогоРабочегоДерева(t *testing.T) {
	text := strings.Join(strings.Fields(CommitPreparation().Text()), " ")
	for _, statement := range []string{
		"Inspect the current Git status, diff, and recent history",
		"Run every applicable repository check",
		"leave the Git working tree clean",
	} {
		if !strings.Contains(text, statement) {
			t.Errorf("поручение не содержит требование %q", statement)
		}
	}
}
