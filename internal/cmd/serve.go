package cmd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"agent-pane/internal/herdrcore"
	"agent-pane/internal/jsonrpc"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

//go:embed all:themes
var themeFs embed.FS

const terminalFontFamily = `"JetBrainsMono Nerd Font Mono", monospace`

const scrollbackBytes = 2 << 20 // 2 MiB

type Session struct {
	ID string
	// term is the session's terminal, made by the manager's backend.
	term terminal

	mu           sync.Mutex
	teardownOnce sync.Once
	cols         int
	rows         int
	closed       bool
	manager      *SessionManager

	// Label names the terminal in approval prompts (agent name or Terminal).
	Label string
	// page is the host's end of the page channel; nil where there is none.
	page *net.UnixConn
	// pageSocket serves the session's processes that did not inherit the
	// page channel; nil where there is none.
	pageSocket *net.UnixListener
	// started is set once the session's process runs. Until then the
	// session only holds its pane tab: lookups and listings skip it.
	started bool
	// pane is the pane tab this session runs in. Page-read grants live
	// there, so they outlive this session when the panel replaces it.
	pane *paneTab
	// done closes with the session, ending reads still in flight.
	done chan struct{}
	// replies are the reply sockets of reads in flight; closing the
	// session closes them, so nothing is delivered after it.
	replies map[*os.File]struct{}
}

// extensionOrigins are the only origins that may open a PTY WebSocket. Add the
// Chrome Web Store build's origin here once it has an id.
var extensionOrigins = []string{
	"chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm", // unpacked dev build (manifest key)
}

// attachToken lets one WebSocket attach to one tty, once. It stays valid until
// it is used, a newer token is issued for the same tty, or the native-messaging
// connection that asked for it closes.
type attachToken struct {
	value []byte
	ttyID string
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	backend  terminalBackend
	port     int
	logger   *slog.Logger

	tokenMu sync.Mutex
	tokens  []attachToken

	callMu sync.Mutex
	call   extensionCaller

	// panes are the pane tabs the extension has open, by id; guarded by mu.
	panes map[string]*paneTab
	// durable is the Durable (preview) backend, used when the
	// terminalBackend setting picks it; nil where there is none.
	durable terminalBackend
	// pageReads counts page reads in flight across all sessions.
	pageReads atomic.Int32
}

func (m *SessionManager) takePageRead() bool {
	if m.pageReads.Add(1) > int32(maxHostPageReads) {
		m.pageReads.Add(-1)
		return false
	}
	return true
}

func (m *SessionManager) releasePageRead() {
	m.pageReads.Add(-1)
}

// SetExtensionCaller sets how page reads reach the extension.
func (m *SessionManager) SetExtensionCaller(call extensionCaller) {
	m.callMu.Lock()
	m.call = call
	m.callMu.Unlock()
}

func (m *SessionManager) extensionCaller() extensionCaller {
	m.callMu.Lock()
	defer m.callMu.Unlock()
	return m.call
}

func NewSessionManager(port int, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		panes:    make(map[string]*paneTab),
		backend:  ptyBackend{logger: logger},
		durable:  newCoreBackend(logger, herdrcore.Private(dataDir)),
		port:     port,
		logger:   logger,
	}
}

// issueToken returns a new one-time attach token for the tty id and drops any
// earlier token for it.
func (m *SessionManager) issueToken(id string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.dropTokensLocked(id)
	m.tokens = append(m.tokens, attachToken{value: raw, ttyID: id})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// redeemToken reports whether token is the live attach token for the tty id.
// A matching token is used up, whether or not it was for this tty.
func (m *SessionManager) redeemToken(id, token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return false
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	found := -1
	for i, t := range m.tokens {
		if subtle.ConstantTimeCompare(t.value, raw) == 1 {
			found = i
		}
	}
	if found < 0 {
		return false
	}
	t := m.tokens[found]
	m.tokens = append(m.tokens[:found], m.tokens[found+1:]...)
	return subtle.ConstantTimeCompare([]byte(t.ttyID), []byte(id)) == 1
}

// dropTokens drops the unused attach tokens for a tty that is gone.
func (m *SessionManager) dropTokens(id string) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.dropTokensLocked(id)
}

func (m *SessionManager) dropTokensLocked(id string) {
	live := m.tokens[:0]
	for _, t := range m.tokens {
		if t.ttyID != id {
			live = append(live, t)
		}
	}
	m.tokens = live
}

// revokeTokens drops every unused attach token. It runs when the
// native-messaging connection that issued them closes.
func (m *SessionManager) revokeTokens() {
	m.tokenMu.Lock()
	m.tokens = nil
	m.tokenMu.Unlock()
}

// endpoint is the tty.create / tty.attach result: the id and a WebSocket URL
// that carries a fresh attach token.
func (m *SessionManager) endpoint(id string) (map[string]string, error) {
	token, err := m.issueToken(id)
	if err != nil {
		return nil, fmt.Errorf("failed to create attach token: %w", err)
	}
	out := map[string]string{
		"url": m.wsURL(id, token),
		"id":  id,
	}
	if s, ok := m.Get(id); ok && s.pane != nil {
		out["pane"] = s.pane.id
	}
	return out, nil
}

func (m *SessionManager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok || !s.isStarted() {
		return nil, false
	}
	return s, true
}

func (m *SessionManager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sessions))
	for id, s := range m.sessions {
		s.mu.Lock()
		live := s.started && !s.closed
		s.mu.Unlock()
		if live {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *SessionManager) wsURL(id, token string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d/tty/%s?token=%s", m.port, id, token)
}

// startHook runs in Create between registering a session and starting it.
// Tests hold a start there.
var startHook = func() {}

// Create starts a session running command in the pane tab paneID (a new pane
// tab when it is empty or cannot be joined). agent is the agent-definition id
// the launch chose, or "" for a shell. The session runs in the backend the
// terminalBackend setting picks; when Durable (preview) cannot start it, it
// runs in the Built-in backend with a notice in the terminal saying why.
func (m *SessionManager) Create(label, paneID, agent string, command *terminalCommand) (*Session, error) {
	id := strings.ToLower(rand.Text())
	session := &Session{
		ID:      id,
		manager: m,
		Label:   label,
	}
	// Register the session before it starts, so its pane tab is held and
	// its environment can name it. It is not live until it has started.
	m.mu.Lock()
	session.pane = m.paneLocked(paneID)
	m.sessions[id] = session
	m.mu.Unlock()

	startHook()
	pid, err := 0, error(nil)
	started := false
	var notice []byte
	if m.durable != nil && cfg().String(terminalBackendKey) == terminalBackendDurable {
		pid, err = m.startIn(m.durable, session, agent, command, nil)
		if err != nil {
			m.logger.Warn("durable terminal backend could not start the terminal; using built-in", "error", err)
			notice = fallbackNotice(err)
		} else {
			started = true
		}
	}
	if !started {
		pid, err = m.startIn(m.backend, session, agent, command, notice)
	}
	if err != nil {
		m.remove(id)
		return nil, fmt.Errorf("failed to start terminal: %w", err)
	}
	session.mu.Lock()
	session.started = true
	session.mu.Unlock()

	// The session is reaped when its program exits or its terminal ends.
	session.term.run(func() { m.reap(id) })
	if session.page != nil {
		go session.servePage(session.page, m.extensionCaller)
	}
	if sock := session.pageSocket; sock != nil && pid > 0 {
		go session.servePageSocket(sock, pid, m.extensionCaller)
	} else if sock != nil {
		// An older macOS spawn agent does not say which process it
		// started, so nothing could be checked against it.
		_ = sock.Close()
	}

	return session, nil
}

// startIn starts command for session in backend, with the session's page
// channel and socket, with notice shown before the program's output. On
// error it leaves the session as it found it.
func (m *SessionManager) startIn(backend terminalBackend, session *Session, agent string, command *terminalCommand, notice []byte) (int, error) {
	term, err := backend.open(session.ID, command)
	if err != nil {
		return 0, err
	}
	if t, ok := term.(*ptyTerminal); ok && notice != nil {
		t.preface(notice)
	}

	// A terminal without a page channel still works; page commands in it
	// say they are not in an Agent Pane terminal.
	var page *net.UnixConn
	var child *os.File
	if backend.inheritsPageChannel() {
		if page, child, err = newPageChannel(); err != nil {
			m.logger.Warn("no page channel for terminal", "error", err)
		}
	}

	// Commands that do not inherit the page channel (a coding agent's
	// command tool) reach the session through this socket instead.
	var sock *net.UnixListener
	sockPath := ""
	if page != nil || !backend.inheritsPageChannel() {
		if sock, sockPath, err = newPageSocket(); err != nil {
			m.logger.Warn("no page socket for terminal", "error", err)
		}
	}

	session.mu.Lock()
	session.term, session.page, session.pageSocket = term, page, sock
	session.mu.Unlock()
	command.Env = withSessionEnv(command.Env, sessionEnv(session, agent, page != nil, sockPath))

	pid, err := term.start(command, child)
	if child != nil {
		_ = child.Close()
	}
	if err != nil {
		session.mu.Lock()
		session.term, session.page, session.pageSocket = nil, nil, nil
		session.mu.Unlock()
		if page != nil {
			_ = page.Close()
		}
		if sock != nil {
			_ = sock.Close()
		}
		_ = term.close()
		return 0, err
	}
	return pid, nil
}

// fallbackNotice tells the person in a Built-in session why it is not
// running in the Durable (preview) backend they chose.
func fallbackNotice(cause error) []byte {
	return []byte(fmt.Sprintf("\x1b[33mAgent Pane: the Durable (preview) terminal backend could not start this terminal (%s). It runs in the Built-in backend instead.\x1b[0m\r\n", strings.ReplaceAll(cause.Error(), "\n", " ")))
}

// Destroy ends a live session. One still starting is not the caller's yet.
func (m *SessionManager) Destroy(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok && !session.isStarted() {
		ok = false
	}
	if ok {
		delete(m.sessions, id)
		m.forgetIdlePaneLocked(session.pane)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("invalid tty ID")
	}
	m.dropTokens(id)
	session.finalize()
	return nil
}

// Close ends every session whose terminal would outlive the host: the
// Durable (preview) ones, which run in the core. Built-in terminals end with
// the host's process.
func (m *SessionManager) Close() {
	m.mu.RLock()
	var ids []string
	for id, s := range m.sessions {
		s.mu.Lock()
		_, durable := s.term.(*coreTerminal)
		s.mu.Unlock()
		if durable {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.remove(id)
	}
}

// reap removes a dead session after its program exits or its terminal ends.
func (m *SessionManager) reap(id string) {
	m.remove(id)
}

// remove drops a session, started or not, and tears it down.
func (m *SessionManager) remove(id string) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		m.forgetIdlePaneLocked(session.pane)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	m.dropTokens(id)
	session.finalize()
}

func (s *Session) finalize() {
	s.teardownOnce.Do(func() {
		s.close()
	})
}

func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	// A descriptor that outlives its terminal stops working.
	if s.page != nil {
		_ = s.page.Close()
	}
	if s.pageSocket != nil {
		_ = s.pageSocket.Close()
	}
	// The pane tab's grants stay: the panel may replace this session in the
	// same pane tab. Closing the pane tab is what revokes them.
	if s.done == nil {
		s.done = make(chan struct{})
	}
	close(s.done)
	// Each tracked reply holds a host-wide read slot; untrackReply finds
	// nothing to release once the map is cleared, so release them here.
	for reply := range s.replies {
		_ = reply.Close()
		if s.manager != nil {
			s.manager.releasePageRead()
		}
	}
	s.replies = nil
	if s.term != nil {
		_ = s.term.close()
	}
}

func (s *Session) isStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func NewCmdServe(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "serve",
		Hidden:       true,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Keep other same-user processes from opening this process's
			// stdin/stdout through /proc and speaking native messaging.
			if err := hardenHost(); err != nil {
				return err
			}
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create log directory: %v\n", err)
				os.Exit(1)
			}

			logFile, err := os.OpenFile(filepath.Join(cacheDir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
				os.Exit(1)
			}
			defer logFile.Close()
			logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{}))

			ln, err := listenLoopback()
			if err != nil {
				return err
			}
			port := ln.Addr().(*net.TCPAddr).Port

			sessions := NewSessionManager(port, logger)
			messagingHost := NewMessagingHost(logger, sessions, version)
			messagingHost.UseInterruptibleStdout()
			sessions.SetExtensionCaller(nativeCaller(messagingHost))

			handler := NewWebSocketHandler(sessions)

			logger.Info("Listening", "addr", ln.Addr().String())

			server := &http.Server{
				Handler: handler,
			}

			done := make(chan error, 1)

			go func() {
				// Chrome starts one host per native-messaging connection, so
				// once it closes no attach token it handed out may be used.
				defer sessions.revokeTokens()
				if err := messagingHost.Listen(); err != nil {
					logger.Error("Messaging host listen loop exited", "error", err)
					done <- err
				} else {
					logger.Info("Messaging host stopped normally")
					done <- nil
				}
			}()

			go func() {
				if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
					logger.Error("HTTP server error", "error", err)
					done <- err
				}
			}()

			// Chrome ends a host it no longer needs with SIGTERM.
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
			go func() {
				<-stop
				done <- nil
			}()

			err = <-done
			logger.Info("Shutting down server")
			server.Shutdown(context.Background())
			// Durable (preview) terminals keep today's rule: they end
			// with the host.
			sessions.Close()
			return err
		},
	}

	return cmd
}

// protocolVersion is raised whenever the extension needs something an older
// host does not have, including a new extension origin the host must accept.
// The extension refuses terminals from a lower protocol.
const protocolVersion = 1

type initializeResult struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

func initializeHandler(logger *slog.Logger, version string) jsonrpc.RequestHandlerFunc {
	return func(input []byte) (any, error) {
		var params struct {
			Version   string `json:"version"`
			BrowserID string `json:"browserId"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal initialize params: %w", err)
		}

		logger.Info("Received initialize notification", "version", params.Version, "browserId", params.BrowserID)
		return initializeResult{Version: version, Protocol: protocolVersion}, nil
	}
}

func NewMessagingHost(logger *slog.Logger, sessions *SessionManager, version string) *jsonrpc.Host {
	messagingHost := jsonrpc.NewHost(logger)

	messagingHost.HandleRequest("initialize", initializeHandler(logger, version))

	messagingHost.HandleRequest("tty.create", func(input []byte) (any, error) {
		var params struct {
			Mode string   `json:"mode"`
			App  string   `json:"app"`
			Args []string `json:"args"`
			Cwd  string   `json:"cwd"`
			File string   `json:"file"`
			// Pane is the pane tab this session replaces a dead one in.
			Pane string `json:"pane"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}

		if len(input) > 0 {
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("failed to unmarshal create params: %w", err)
			}
		}

		mode, app := params.Mode, params.App
		label := "Terminal"
		switch mode {
		case "shell":
			mode, app = "", ""
		case "app":
			if !isSafeAppID(app) {
				return nil, fmt.Errorf("invalid app id %q", app)
			}
			agent, ok := agentByID(app)
			if !ok {
				return nil, fmt.Errorf("unknown app %q", app)
			}
			label = agent.Name
		default:
			mode, app = "", ""
		}

		command, err := buildTerminalCommand(mode, app, params.Args, params.Cwd)
		if err != nil {
			return nil, err
		}
		command.Cols, command.Rows = params.Cols, params.Rows
		session, err := sessions.Create(label, params.Pane, app, command)
		if err != nil {
			return nil, err
		}

		return sessions.endpoint(session.ID)
	})

	messagingHost.HandleRequest("tty.attach", func(input []byte) (any, error) {
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attach params: %w", err)
		}
		if params.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		session, ok := sessions.Get(params.ID)
		if !ok {
			return nil, fmt.Errorf("invalid tty ID")
		}
		if session.isClosed() {
			return nil, fmt.Errorf("tty session is closed")
		}

		return sessions.endpoint(session.ID)
	})

	messagingHost.HandleRequest("tty.list", func(input []byte) (any, error) {
		ids := sessions.List()
		items := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]string{"id": id})
		}
		return map[string]any{"sessions": items}, nil
	})

	messagingHost.HandleRequest("tty.destroy", func(input []byte) (any, error) {
		var params struct {
			ID string `json:"id"`
			// Pane, when set, is the pane tab being closed: its grants end
			// even if its session has already died.
			Pane string `json:"pane"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal destroy params: %w", err)
		}
		if params.Pane != "" {
			sessions.ClosePane(params.Pane)
		}
		if err := sessions.Destroy(params.ID); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	})

	messagingHost.HandleRequest("tty.resize", func(input []byte) (any, error) {
		var requestParams struct {
			TTY  string `json:"tty"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if err := json.Unmarshal(input, &requestParams); err != nil {
			return nil, fmt.Errorf("failed to unmarshal resize params: %w", err)
		}

		session, ok := sessions.Get(requestParams.TTY)
		if !ok {
			return nil, fmt.Errorf("invalid tty ID")
		}

		if err := session.term.resize(requestParams.Cols, requestParams.Rows); err != nil {
			return nil, fmt.Errorf("failed to set size for tty: %w", err)
		}
		session.mu.Lock()
		session.cols = requestParams.Cols
		session.rows = requestParams.Rows
		session.mu.Unlock()

		return map[string]any{}, nil
	})

	messagingHost.HandleRequest("xterm.getConfig", func(input []byte) (any, error) {
		var params struct {
			Variant string `json:"variant"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal xterm config params: %w", err)
		}

		var theme string
		if darkTheme := cfg().String("themeDark"); params.Variant == "dark" && darkTheme != "" {
			theme = darkTheme
		} else {
			theme = cfg().String("theme")
		}

		themeBytes, err := themeFs.ReadFile(filepath.Join("themes", theme+".json"))
		if err != nil {
			return nil, fmt.Errorf("failed to read theme file: %w", err)
		}

		xtermConfig := map[string]interface{}{
			"cursorBlink":                   true,
			"cursorStyle":                   "block",
			"allowProposedApi":              true,
			"macOptionIsMeta":               true,
			"macOptionClickForcesSelection": true,
			"fontSize":                      13,
			"fontFamily":                    terminalFontFamily,
			"theme":                         json.RawMessage(themeBytes),
		}

		if err := cfg().Unmarshal("xterm", &xtermConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal xterm config: %w", err)
		}
		if family, _ := xtermConfig["fontFamily"].(string); family == "" {
			xtermConfig["fontFamily"] = terminalFontFamily
		}

		return xtermConfig, nil
	})

	messagingHost.HandleRequest("readFile", func(input []byte) (any, error) {
		var params struct {
			Path string `json:"path"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal readFile params: %w", err)
		}

		content, err := os.ReadFile(params.Path)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			return map[string]any{
				"content": "",
			}, nil
		} else if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}

		return map[string]any{
			"content": string(content),
		}, nil
	})

	messagingHost.HandleRequest("writeFile", func(input []byte) (any, error) {
		var params struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal writeFile params: %w", err)
		}

		f, err := os.OpenFile(params.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open file for writing: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(params.Content); err != nil {
			return nil, fmt.Errorf("failed to write file content: %w", err)
		}

		return map[string]any{}, nil
	})

	messagingHost.HandleRequest("dialog.chooseFolder", func(input []byte) (any, error) {
		path, err := chooseFolder()
		if err != nil {
			return nil, err
		}
		return map[string]string{"path": path}, nil
	})

	messagingHost.HandleRequest("config.get", func(input []byte) (any, error) {
		cfg, err := readConfigMap()
		if err != nil {
			return nil, err
		}
		themes, _ := listThemeNames()
		return map[string]any{
			"config": cfg,
			"themes": themes,
			"agents": catalog(),
			"path":   configPath(),
		}, nil
	})

	messagingHost.HandleRequest("config.set", func(input []byte) (any, error) {
		var params struct {
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config.set params: %w", err)
		}
		if params.Config == nil {
			return nil, fmt.Errorf("config is required")
		}
		if err := writeConfigMap(params.Config); err != nil {
			return nil, err
		}
		cfg, err := readConfigMap()
		if err != nil {
			return nil, err
		}
		return map[string]any{"config": cfg}, nil
	})

	return messagingHost
}

const chooseFolderScript = `try
	POSIX path of (choose folder with prompt "Launch in a folder")
on error errMsg number errNum
	if errNum is -128 then
		return ""
	end if
	error errMsg number errNum
end try`

func chooseFolder() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("folder picker is not available")
	}
	out, err := exec.Command("osascript", "-e", chooseFolderScript).Output()
	return folderPick(out, err)
}

func folderPick(stdout []byte, runErr error) (string, error) {
	if runErr != nil {
		detail := runErr.Error()
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && len(exitErr.Stderr) > 0 {
			detail = string(exitErr.Stderr)
		}
		if strings.Contains(detail, "User canceled") || strings.Contains(detail, "-128") {
			return "", nil
		}
		return "", fmt.Errorf("folder picker: %s", strings.TrimSpace(detail))
	}
	return strings.TrimSpace(string(stdout)), nil
}

func listThemeNames() ([]string, error) {
	entries, err := themeFs.ReadDir("themes")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		names = append(names, name)
	}
	return names, nil
}

var safeAppIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func isSafeAppID(app string) bool {
	return app != "" && safeAppIDRe.MatchString(app)
}

func buildTerminalCommand(mode, app string, args []string, cwd string) (*terminalCommand, error) {
	var cmd *terminalCommand
	overlay := map[string]string{
		"TERM": "xterm-256color",
	}
	if mode == "app" && app != "" {
		agent, ok := agentByID(app)
		if !ok {
			return nil, fmt.Errorf("unknown app %q", app)
		}
		shell := configuredShell()
		argv := append([]string{agent.Bin}, agent.Args...)
		argv = append(argv, args...)
		cmd = newTerminalCommand(shell, agentExecArgs(shell, argv)...)
	} else {
		shell := cfg().String("command")
		if shell == "" {
			shell = getDefaultShell()
		}
		cmd = newTerminalCommand(shell, loginShellArgs(shell, cfg().Strings("args"))...)
	}

	cmd.Env = mergePtyEnv(os.Environ(), overlay, cfg().StringMap("env"))
	cmd.Dir = sessionDir(cwd, cfg().String("cwd"), mode == "app" && app != "")

	return cmd, nil
}

// sessionDir is the request directory, or the configured agent folder, or home.
// A leading ~ is the home directory. The shell tab does not use the agent folder.
func sessionDir(requested, configured string, agent bool) string {
	dir := requested
	if dir == "" && agent {
		dir = strings.TrimSpace(configured)
	}
	if dir == "" {
		return os.Getenv("HOME")
	}
	if dir == "~" {
		return os.Getenv("HOME")
	}
	if strings.HasPrefix(dir, "~/") {
		return filepath.Join(os.Getenv("HOME"), dir[2:])
	}
	return dir
}

// loginShellArgs makes zsh, bash, and fish start as login shells unless the
// configured args already say so.
func loginShellArgs(shell string, args []string) []string {
	switch filepath.Base(shell) {
	case "zsh", "bash", "fish":
	default:
		return args
	}
	for _, arg := range args {
		if arg == "-l" || arg == "--login" {
			return args
		}
	}
	return append([]string{"-l"}, args...)
}

// mergePtyEnv builds the PTY environment. Later maps win, and a key replaces
// any earlier entry so the first duplicate is not the one getenv returns.
func mergePtyEnv(base []string, overlays ...map[string]string) []string {
	envMap := map[string]string{}
	order := make([]string, 0, len(base)+8)
	for _, e := range base {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, exists := envMap[key]; !exists {
			order = append(order, key)
		}
		envMap[key] = val
	}
	for _, overlay := range overlays {
		for key, val := range overlay {
			if _, exists := envMap[key]; !exists {
				order = append(order, key)
			}
			envMap[key] = val
		}
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+envMap[key])
	}
	return out
}

// NewWebSocketHandler serves /tty/<id>?token=<attach token>. The origin and the
// token are checked before the session is looked up or any replay is sent, and
// refusals do not say which check failed or echo the id or token.
func NewWebSocketHandler(sessions *SessionManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ttyID, ok := strings.CutPrefix(r.URL.Path, "/tty/")
		if !ok || !sessions.redeemToken(ttyID, r.URL.Query().Get("token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		session, ok := sessions.Get(ttyID)
		if !ok || session.isClosed() {
			http.Error(w, "terminal is gone", http.StatusGone)
			return
		}

		HandleWebsocket(session)(w, r)
	})
}

func HandleWebsocket(session *Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		upgrader := getConnectionUpgrader(maxBufferSizeBytes)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("failed to upgrade connection: %s", err)
			return
		}
		defer connection.Close()

		// Always send one binary replay frame (possibly empty) so the client
		// can gate onData until xterm finishes parsing it — prevents DA replies
		// from scrollback CSI queries leaking into the PTY as "1;2c".
		viewer := &wsViewer{conn: connection}
		detach, err := session.term.attach(viewer)
		if err != nil {
			log.Printf("failed to replay scrollback: %s", err)
			return
		}
		defer detach()

		lastPingTime := time.Now()
		connection.SetPongHandler(func(appData string) error {
			lastPingTime = time.Now()
			return nil
		})

		done := make(chan struct{})

		go func() {
			ticker := time.NewTicker(keepalivePingTimeout / 2)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if err := viewer.ping(); err != nil {
						return
					}
					if time.Since(lastPingTime) > keepalivePingTimeout {
						connection.Close()
						return
					}
				}
			}
		}()

		// Client input → terminal (binary only; ignore text control frames)
		for {
			msgType, data, err := connection.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			if msgType != websocket.BinaryMessage {
				continue
			}
			if session.isClosed() {
				close(done)
				return
			}
			if _, err := session.term.write(data); err != nil {
				log.Printf("failed to write to tty: %s", err)
				close(done)
				return
			}
		}
	}
}

// wsViewer is a WebSocket attached to a terminal. mu serializes its writes
// (replay, live output, ping).
type wsViewer struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (v *wsViewer) send(p []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conn.WriteMessage(websocket.BinaryMessage, p)
}

func (v *wsViewer) ping() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conn.WriteMessage(websocket.PingMessage, []byte("keepalive"))
}

func (v *wsViewer) close() error {
	return v.conn.Close()
}

func getConnectionUpgrader(
	maxBufferSizeBytes int,
) websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin:      allowedOrigin,
		HandshakeTimeout: 0,
		ReadBufferSize:   maxBufferSizeBytes,
		WriteBufferSize:  maxBufferSizeBytes,
	}
}

// allowedOrigin accepts only the extension's own origins. A request with no
// Origin header (a local process, not a browser page) is refused too.
func allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	for _, allowed := range extensionOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// listenLoopback binds the PTY socket on 127.0.0.1. The side panel is given that address.
func listenLoopback() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen on 127.0.0.1: %w", err)
	}
	return ln, nil
}
