//go:build windows

package cmd

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// startProbe starts cmd in a job object that ends every process left in it
// when end runs or the probe's context ends. Ending cmd.exe alone would leave
// the node an npm shim started running.
func startProbe(cmd *exec.Cmd) (end func(), err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, cmd.Start()
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return func() {}, cmd.Start()
	}
	cmd.Cancel = func() error {
		windows.TerminateJobObject(job, 1)
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		windows.AssignProcessToJobObject(job, proc)
		windows.CloseHandle(proc)
	}
	return func() { windows.CloseHandle(job) }, nil
}
