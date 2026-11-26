package ipc

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agy-switch/internal/store"
)

const lockFileName = "instance.addr"

// Server 提供本地回环单实例唤醒与 CLI -> 托盘状态实时同步通知
type Server struct {
	ln       net.Listener
	lockPath string
}

// TryStartServer 尝试作为主托盘实例监听回环端口；若已有实例在线则返回 (nil, true, nil) 并自动唤醒旧实例窗口
func TryStartServer(baseDir string, onCommand func(cmd string)) (*Server, bool, error) {
	lockPath := filepath.Join(baseDir, lockFileName)
	if NotifyExisting(baseDir, "SHOW") {
		return nil, true, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, false, err
	}
	addr := ln.Addr().String()
	_ = store.AtomicWriteFile(lockPath, []byte(addr), 0o600)

	srv := &Server{ln: ln, lockPath: lockPath}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				sc := bufio.NewScanner(c)
				if sc.Scan() {
					cmd := strings.TrimSpace(sc.Text())
					_, _ = fmt.Fprintln(c, "OK")
					if onCommand != nil && cmd != "" {
						onCommand(cmd)
					}
				}
			}(conn)
		}
	}()
	return srv, false, nil
}

func (s *Server) Close() {
	if s == nil {
		return
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.lockPath != "" {
		_ = os.Remove(s.lockPath)
	}
}

// NotifyExisting 向运行中的托盘实例发送控制指令 (如 "SHOW" 或 "RELOAD")，成功返回 true
func NotifyExisting(baseDir, command string) bool {
	if strings.TrimSpace(baseDir) == "" {
		var err error
		baseDir, err = store.DefaultBaseDir()
		if err != nil {
			return false
		}
	}
	raw, err := os.ReadFile(filepath.Join(baseDir, lockFileName))
	if err != nil {
		return false
	}
	addr := strings.TrimSpace(string(raw))
	if addr == "" || !strings.HasPrefix(addr, "127.0.0.1:") {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 350*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(800 * time.Millisecond))
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return false
	}
	sc := bufio.NewScanner(conn)
	return sc.Scan() && strings.TrimSpace(sc.Text()) == "OK"
}
