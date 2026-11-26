//go:build windows

package agy

import (
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const createNoWindow = 0x08000000

func hideBackgroundWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// CountRunningAgyProcesses 使用 Win32 Toolhelp32 快照毫秒级统计正在运行的 agy.exe 数量（零子进程闪烁）
func CountRunningAgyProcesses() int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}

	selfPID := uint32(windows.GetCurrentProcessId())
	count := 0
	for {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if pe.ProcessID != selfPID && strings.EqualFold(name, "agy.exe") {
			count++
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return count
}

const runRegistryKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runRegistryValueName = "AGYSwitch"

func setOSAutoStart(enabled bool, exePath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runRegistryKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if !enabled {
		err := k.DeleteValue(runRegistryValueName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	cmdStr := `"` + exePath + `" --minimized`
	return k.SetStringValue(runRegistryValueName, cmdStr)
}

func getOSAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runRegistryKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(runRegistryValueName)
	return err == nil && strings.TrimSpace(val) != ""
}
