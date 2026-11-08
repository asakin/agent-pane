package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The environment Agent Pane gives every PTY session it starts. These keys
// are the host's: an inherited value (a host started from inside a pane tab,
// a stale export) or a configured "env" entry never replaces them, and a key
// with no value for this session is removed rather than inherited.
const (
	envPresence    = "AGENT_PANE"
	envTermProgram = "TERM_PROGRAM"
	envTabID       = "AGENT_PANE_TAB_ID"
	envSessionID   = "AGENT_PANE_SESSION_ID"
	envAgent       = "AGENT_PANE_AGENT"
	envSkillPath   = "AGENT_PANE_SKILL_PATH"
	envSocket      = "AGENT_PANE_SOCKET"
)

// reservedEnv lists every key withSessionEnv owns. TERM_PROGRAM_VERSION is
// dropped so a version from the terminal the host started in does not sit
// beside TERM_PROGRAM=agent-pane.
var reservedEnv = []string{
	envPresence,
	envTermProgram,
	"TERM_PROGRAM_VERSION",
	envTabID,
	envSessionID,
	envAgent,
	pageFDEnv,
	envSocket,
	envSkillPath,
}

// skillPath is where install.sh puts the canonical Agent Pane skill.
var skillPath = filepath.Join(dataDir, "skills", "agent-pane", "SKILL.md")

// sessionEnv is the reserved environment for one session. agent is the
// agent-definition id the launch chose, "" for a shell or custom command;
// socket is the session's page socket, "" when it has none.
func sessionEnv(s *Session, agent string, hasPage bool, socket string) map[string]string {
	env := map[string]string{
		envPresence:    "1",
		envTermProgram: "agent-pane",
		envSessionID:   publicID("session", s.ID),
	}
	if s.pane != nil {
		env[envTabID] = publicID("tab", s.pane.id)
	}
	if agent != "" {
		env[envAgent] = agent
	}
	if hasPage {
		env[pageFDEnv] = fmt.Sprint(pageFD)
	}
	if socket != "" {
		env[envSocket] = socket
	}
	if info, err := os.Stat(skillPath); err == nil && info.Mode().IsRegular() {
		env[envSkillPath] = skillPath
	}
	return env
}

// withSessionEnv removes every reserved key from base and adds reserved.
// Other keys, PATH included, keep their order and values.
func withSessionEnv(base []string, reserved map[string]string) []string {
	out := make([]string, 0, len(base)+len(reserved))
	for _, e := range base {
		key, _, _ := strings.Cut(e, "=")
		if !isReservedEnv(key) {
			out = append(out, e)
		}
	}
	for _, key := range reservedEnv {
		if val, ok := reserved[key]; ok {
			out = append(out, key+"="+val)
		}
	}
	return out
}

func isReservedEnv(key string) bool {
	for _, k := range reservedEnv {
		if key == k {
			return true
		}
	}
	return false
}
