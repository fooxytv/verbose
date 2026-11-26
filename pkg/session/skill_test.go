package session

import (
	"strings"
	"testing"
)

const skillCall = `{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00.000Z","message":{"role":"user","content":"tidy this up"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-01-01T10:00:01.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"s1","name":"Skill","input":{"skill":"simplify","args":"only the parser"}}]}}
`

const skillResult = `{"type":"user","uuid":"u2","timestamp":"2026-01-01T10:00:02.000Z","toolUseResult":{"success":true,"commandName":"simplify"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","content":"Launching skill: simplify"}]}}
`

const skillBody = `{"type":"user","uuid":"m1","isMeta":true,"sourceToolUseID":"s1","timestamp":"2026-01-01T10:00:02.001Z","message":{"role":"user","content":[{"type":"text","text":"# Simplify\n\nLook for code that can be reused."}]}}
`

// Loading a skill writes the skill's text as a user-role entry. It is what the
// skill call produced, so it belongs on the call — shown as a prompt, it read
// as if the person had typed the whole skill.
func TestLoadedSkillTextIsTheCallsOutput(t *testing.T) {
	for name, body := range map[string]string{
		"after its result":  skillCall + skillResult + skillBody,
		"before its result": skillCall + skillBody + skillResult,
	} {
		sess := fullParse(t, writeTranscript(t, body))

		if sess.Info.UserPrompts != 1 {
			t.Fatalf("%s: %d user prompts, want 1", name, sess.Info.UserPrompts)
		}
		var call *Event
		for i := range sess.Events {
			e := &sess.Events[i]
			if e.Type == EventUserPrompt && strings.Contains(e.UserText, "Simplify") {
				t.Fatalf("%s: the skill's text is a user prompt", name)
			}
			if e.Type == EventToolUse && e.ToolID == "s1" {
				call = e
			}
		}
		if call == nil {
			t.Fatalf("%s: no skill call", name)
		}
		if !strings.HasPrefix(call.ToolOutput, "Launching skill: simplify") ||
			!strings.Contains(call.ToolOutput, "Look for code that can be reused.") {
			t.Fatalf("%s: output %q, want the result then the skill's text", name, call.ToolOutput)
		}
		if got := DescribeEvent(*call, ""); !strings.Contains(got, "only the parser") {
			t.Fatalf("%s: description %q leaves out the args", name, got)
		}
	}
}

// An isMeta entry with no call to belong to is left as it was.
func TestMetaTextWithoutACallIsUnchanged(t *testing.T) {
	body := `{"type":"user","uuid":"m1","isMeta":true,"timestamp":"2026-01-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"caveat"}]}}` + "\n"
	sess := fullParse(t, writeTranscript(t, body))
	if len(sess.Events) != 1 || sess.Events[0].Type != EventUserPrompt {
		t.Fatalf("events %+v, want the one prompt", sess.Events)
	}
}
