//go:build windows

package cmd

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// startProbe starts cmd in a job object that ends every process left in it
// when end runs or the probe's context ends. Ending cmd.exe alone would leave
// the node an npm shim started running. cmd starts suspended and runs only
// once it is in the job, so nothing it starts can be outside it. When the job
// cannot be set up the probe does not run, and the error says why.
func startProbe(cmd *exec.Cmd) (end func(), err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create a job object for it: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("could not set up a job object for it: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.Cancel = func() error {
		windows.TerminateJobObject(job, 1)
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err := assignAndResume(job, uint32(cmd.Process.Pid)); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	return func() { windows.CloseHandle(job) }, nil
}

// assignAndResume puts the suspended process pid in job, then resumes its
// threads.
func assignAndResume(job windows.Handle, pid uint32) error {
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("could not open it to add it to a job object: %w", err)
	}
	err = windows.AssignProcessToJobObject(job, proc)
	windows.CloseHandle(proc)
	if err != nil {
		return fmt.Errorf("could not add it to a job object: %w", err)
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("could not list its threads to resume it: %w", err)
	}
	defer windows.CloseHandle(snap)
	resumed := 0
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("could not open its thread to resume it: %w", err)
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if err != nil {
			return fmt.Errorf("could not resume it: %w", err)
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("could not find its thread to resume it")
	}
	return nil
}
