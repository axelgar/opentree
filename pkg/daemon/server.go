package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/axelgar/opentree/pkg/workspace"
)

// Server is the daemon process that owns all PTYs and responds to client requests.
type Server struct {
	pm       *workspace.NativeProcessManager
	sockPath string
	pidPath  string
	listener net.Listener
	logger   *log.Logger
}

// NewServer creates a new daemon server for the given repo root.
func NewServer(repoRoot string) *Server {
	opentreeDir := filepath.Join(repoRoot, ".opentree")
	return &Server{
		pm:       workspace.NewNativeProcessManager(0, 0),
		sockPath: filepath.Join(opentreeDir, "daemon.sock"),
		pidPath:  filepath.Join(opentreeDir, "daemon.pid"),
		logger:   log.New(os.Stderr, "[daemon] ", log.LstdFlags),
	}
}

// Start starts the daemon server, listening on the Unix socket.
// This blocks until the server is shut down via signal.
func (s *Server) Start() error {
	if err := os.MkdirAll(filepath.Dir(s.sockPath), 0755); err != nil {
		return fmt.Errorf("failed to create .opentree dir: %w", err)
	}

	// Remove stale socket if it exists.
	os.Remove(s.sockPath)

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.sockPath, err)
	}
	s.listener = ln

	if err := os.WriteFile(s.pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		ln.Close()
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Handle signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.shutdown()
	}()

	s.logger.Printf("daemon started, pid=%d, socket=%s", os.Getpid(), s.sockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener was closed during shutdown.
			return nil
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) shutdown() {
	s.logger.Println("shutting down...")
	s.pm.KillSession()
	os.Remove(s.sockPath)
	os.Remove(s.pidPath)
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			s.writeResponse(conn, Response{Error: "invalid request: " + err.Error()})
			continue
		}
		resp := s.dispatch(req)
		s.writeResponse(conn, resp)
	}
}

func (s *Server) writeResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return
		}
		data = data[n:]
	}
}

func (s *Server) dispatch(req Request) Response {
	// Reject requests with an explicit but mismatched protocol version.
	// An empty version field is accepted for backward compatibility with
	// clients that predate versioning.
	if req.Version != "" && req.Version != ProtocolVersion {
		return Response{ID: req.ID, Error: fmt.Sprintf("protocol version mismatch: client=%q server=%q", req.Version, ProtocolVersion)}
	}

	switch req.Method {
	case MethodPing:
		return Response{ID: req.ID}

	case MethodCreateWindow:
		var p CreateWindowParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.CreateWindow(p.Name, p.Workdir, p.Command, p.Args...); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodCreateWindowSized:
		var p CreateWindowSizedParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.CreateWindowSized(p.Name, p.Workdir, p.Command, p.Cols, p.Rows, p.Args...); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodListWindows:
		windows, err := s.pm.ListWindows()
		if err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return s.resultResponse(req.ID, ListWindowsResult{Windows: windows})

	case MethodKillWindow:
		var p NameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.KillWindow(p.Name); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodKillSession:
		if err := s.pm.KillSession(); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodCapturePane:
		var p CapturePaneParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		output, err := s.pm.CapturePane(p.Name, p.Lines)
		if err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return s.resultResponse(req.ID, CapturePaneResult{Output: output})

	case MethodGetWindowActivity:
		var p NameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		t, err := s.pm.GetWindowActivity(p.Name)
		if err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return s.resultResponse(req.ID, GetWindowActivityResult{LastActivity: t})

	case MethodSendMessage:
		var p SendMessageParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.SendMessage(p.Name, p.Text); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodRenderScreen:
		var p NameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		screen, err := s.pm.RenderScreen(p.Name)
		if err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return s.resultResponse(req.ID, RenderScreenResult{Screen: screen})

	case MethodResizeWindow:
		var p ResizeWindowParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.ResizeWindow(p.Name, p.Cols, p.Rows); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodWriteInput:
		var p WriteInputParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		if err := s.pm.WriteInput(p.Name, p.Data); err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return Response{ID: req.ID}

	case MethodIsRunning:
		var p NameParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		running := s.pm.IsRunning(p.Name)
		return s.resultResponse(req.ID, IsRunningResult{Running: running})

	case MethodScrollbackLines:
		var p ScrollbackLinesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, Error: "invalid params: " + err.Error()}
		}
		lines, err := s.pm.ScrollbackLines(p.Name, p.Offset, p.Count)
		if err != nil {
			return Response{ID: req.ID, Error: err.Error()}
		}
		return s.resultResponse(req.ID, ScrollbackLinesResult{Lines: lines})

	default:
		return Response{ID: req.ID, Error: "unknown method: " + req.Method}
	}
}

func (s *Server) resultResponse(id int, result interface{}) Response {
	data, err := json.Marshal(result)
	if err != nil {
		return Response{ID: id, Error: "failed to marshal result: " + err.Error()}
	}
	return Response{ID: id, Result: data}
}
