package main

import (
	"encoding/json"
	"strings"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Atlassian Document Format (ADF) helpers. Jira Cloud's REST API v3 takes
// issue descriptions and comment bodies as ADF documents.

type adfNode = map[string]any

func adfText(s string) adfNode { return adfNode{"type": "text", "text": s} }

// adfParagraph turns a block of text into one paragraph, keeping single
// newlines as hard breaks.
func adfParagraph(block string) adfNode {
	var content []adfNode
	for i, line := range strings.Split(block, "\n") {
		if i > 0 {
			content = append(content, adfNode{"type": "hardBreak"})
		}
		if line != "" {
			content = append(content, adfText(line))
		}
	}
	p := adfNode{"type": "paragraph"}
	if len(content) > 0 {
		p["content"] = content
	}
	return p
}

// markdownToADF converts a ticket description to ADF: blank-line separated
// blocks become paragraphs. The Markdown itself is kept verbatim (it is also
// stored in the graphops.ticket issue property, which is what the plugin
// reads back), so this only needs to be readable in Jira, not lossless.
func markdownToADF(md string) adfNode {
	var blocks []adfNode
	normalized := strings.ReplaceAll(md, "\r\n", "\n")
	for _, block := range strings.Split(normalized, "\n\n") {
		block = strings.Trim(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		blocks = append(blocks, adfParagraph(block))
	}
	if len(blocks) == 0 {
		blocks = []adfNode{{"type": "paragraph"}}
	}
	return adfNode{"type": "doc", "version": 1, "content": blocks}
}

// artifactCommentBody is the human-readable comment written for an
// artifact: its name, type and node, and -- when it is stored inline -- its
// content in a code block.
func artifactCommentBody(a Artifact, inlineContent *string, attachmentName string) adfNode {
	blocks := []adfNode{
		{"type": "paragraph", "content": []adfNode{
			{"type": "text", "text": "GraphOps artifact: ", "marks": []adfNode{{"type": "strong"}}},
			adfText(a.Name),
		}},
		adfParagraph("type: " + a.Type + "\nnode: " + a.NodeID),
	}
	switch {
	case inlineContent != nil && *inlineContent != "":
		lang := ""
		switch a.Type {
		case "json":
			lang = "json"
		case "gherkin":
			lang = "gherkin"
		case "text":
			lang = "markdown"
		}
		cb := adfNode{"type": "codeBlock", "content": []adfNode{adfText(*inlineContent)}}
		if lang != "" {
			cb["attrs"] = adfNode{"language": lang}
		}
		blocks = append(blocks, cb)
	case attachmentName != "":
		blocks = append(blocks, adfParagraph("content: attachment "+attachmentName))
	}
	return adfNode{"type": "doc", "version": 1, "content": blocks}
}
