package prompts

import _ "embed"

//go:embed commit_preparation.md
var commitPreparationText string

type CommitPreparationPrompt struct {
	text string
}

func CommitPreparation() CommitPreparationPrompt {
	return CommitPreparationPrompt{text: commitPreparationText}
}

func (prompt CommitPreparationPrompt) Text() string {
	return prompt.text
}
