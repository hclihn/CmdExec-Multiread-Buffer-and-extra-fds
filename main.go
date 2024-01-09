package main

import (
  "fmt"
  "os"
  "io"
  "os/exec"
  "strings"
  "time"
  "context"
  "errors"
  "sync"
  "math/rand"
  "bytes"
  "syscall"
)

const (
	readSleep = 100 * time.Millisecond
)

func logOutput(buf *MultiReadBuffer, done <-chan struct{}, wg *sync.WaitGroup) {
loop:  
  for {
    select {
      case <-done:
        break loop
      default:
        if buf.Len() > 0 {
          fmt.Printf("%s", buf.ReadString())
        } else {
          if rand.Intn(10) > 8 { // simulate log upload
            fmt.Printf("*")
            time.Sleep(2200 * time.Millisecond)
          } else {
            fmt.Printf(".")
            time.Sleep(readSleep)
          }
        }
    }
  }
  if buf.Len() > 0 {
    fmt.Printf("!%s", buf.ReadString())
  }
  fmt.Println()
  wg.Done()
}

func ExecCmd(ctx context.Context, d time.Duration, theCmd **exec.Cmd, input string, extraFiles []*os.File, command string, args ...string) (string, error) {
  ctx1 := ctx
  if ctx1 == nil {
    ctx1 = context.Background()
  }
	ctx1, cancel := context.WithTimeout(ctx1, d)
	defer cancel()
  cmd := exec.CommandContext(ctx1, command, args...)
  if theCmd != nil {
    *theCmd = cmd
  }
  cmdLine := strings.Join(append([]string{command}, args...), " ")

  if input != "" { // we have input to send
		fmt.Printf("Command input (via stdin): %q\n", input)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return "", fmt.Errorf("executing command (%s) failed when getting its stdin: %w", cmdLine, err)
		}
		// send input to stdin
		go func() {
			defer stdin.Close()
			io.WriteString(stdin, input)
		}()
  }

  var buf MultiReadBuffer
  var wg sync.WaitGroup
  done := make(chan struct{})
  cmd.Stdout = &buf
  cmd.Stderr = cmd.Stdout // combine stderr with stdout
  if extraFiles != nil {
    cmd.ExtraFiles = extraFiles
  }
  wg.Add(1)
  go logOutput(&buf, done, &wg)

  err := cmd.Run()
  done <- struct{}{}
  if theCmd != nil {
    *theCmd = nil
  }
  wg.Wait()
	if err != nil {
    if ctx1.Err() != nil {
      return buf.String(), fmt.Errorf("command (%s) execution failed (%s): %w", cmdLine, ctx1.Err(), err)
    }
		return buf.String(), fmt.Errorf("command (%s) execution failed: %w", cmdLine, err)
	}
	return buf.String(), nil
}

func printExitCode(err error) {
  rc := 0
  var ee *exec.ExitError
  if errors.As(err, &ee) { // need to use errors.As to unwrap the underlying ExitError
		rc = ee.ExitCode()
    if status, ok := ee.Sys().(syscall.WaitStatus); ok { // for unix
			fmt.Printf("*** Signaled: %v\n", status.Signaled())
		}
  }
  fmt.Printf("*** Exit Code: %d\n", rc)
}

func Test(ctx context.Context, d time.Duration, theCmd **exec.Cmd, input string, hasDataOut bool, command string, args ...string) {
  var r, w *os.File
  var err, outErr error
  var dataOut bytes.Buffer

  if hasDataOut {
    r, w, err = os.Pipe()
    if err != nil {
      fmt.Printf("ERROR: failed to create pipe: %v!\n", err)
      return
    }
    go func() {
      _, outErr = io.Copy(&dataOut, r)
      defer r.Close()
    }()
    defer w.Close()
  }

  out, err := ExecCmd(ctx, d, theCmd, input, []*os.File{w}, command, args...)
  fmt.Printf("*** Output:\n%s\n*** Error: %v\n", out, err)
  if hasDataOut {
    if outErr != nil {
      fmt.Printf("ERROR: failed to read pipe for data out: %v!\n", outErr)
    }
    fmt.Printf("*** Data out: %q\n", dataOut.String())
  }
  printExitCode(err)
}

func main() {
  var TheCmd *exec.Cmd

  testScript := `CNT="$(cat -)"; { >&3; HAS_OUT="$?"; } 2<> /dev/null; echo "Data in: $CNT"; echo "Has no data out: $HAS_OUT"; CNT=${CNT:-10}; echo -n test start...; for i in $(seq 1 $CNT); do echo -n $i; if [[ $HAS_OUT -eq 0 ]]; then echo test $i >&3; fi; sleep 1; done; echo end test`

  fmt.Println("Test 1: data in and out")
  Test(nil, 20*time.Second, nil, "7", true, "bash", "-c", testScript)
  fmt.Println()

  fmt.Println("Test 2: data in only")
  Test(nil, 20*time.Second, nil, "7", false, "bash", "-c", testScript)
  fmt.Println()

  fmt.Println("Test 3: data out only")
  Test(nil, 20*time.Second, nil, "", true, "bash", "-c", testScript)
  fmt.Println()
  
  fmt.Println("Test 4: no data in and out")
  Test(nil, 20*time.Second, nil, "", false, "bash", "-c", testScript)
  fmt.Println()

  fmt.Println("Test 5: data in and out with timeout (5 sec)")
  Test(nil, 5*time.Second, nil, "8", true, "bash", "-c", testScript)
  fmt.Println()

  fmt.Println("Test 6: data in and out, but terminated by outside (5 sec)")
  go func() {
    time.Sleep(5*time.Second)
    //fmt.Println("PID:", TheCmd.Process.Pid)
    //TheCmd.Process.Signal(os.Interrupt): doesn't work!
    if TheCmd != nil && TheCmd.Process != nil {
      fmt.Println("@@@Terminate subprocess...@@@")
      TheCmd.Process.Signal(os.Kill)
    } else {
      fmt.Println("@@@No subprocess to terminate!@@@")
    }
  }()
  Test(nil, 20*time.Second, &TheCmd, "8", true, "bash", "-c", testScript)
  TheCmd = nil
  fmt.Println()

  fmt.Println("Test 7: data in and out, but cancelled by outside (3 sec)")
  ctx, cancel := context.WithCancel(context.Background())
  go func() {
    time.Sleep(3*time.Second)
    //fmt.Println("PID:", TheCmd.Process.Pid)
    //TheCmd.Process.Signal(os.Interrupt): doesn't work!
    fmt.Println("@@@Cancel subprocess...@@@")
    cancel()
  }()
  Test(ctx, 20*time.Second, nil, "8", true, "bash", "-c", testScript)
  fmt.Println()

  fmt.Println("Test 8: data in and out, w/o out data")
  Test(nil, 20*time.Second, nil, "7", true, "bash", "-c", `CNT="$(cat -)"; { >&3; HAS_OUT="$?"; } 2<> /dev/null; echo "Data in: $CNT"; echo "Has no data out: $HAS_OUT"; CNT=${CNT:-10}; echo -n test start...; for i in $(seq 1 $CNT); do echo -n $i; sleep 1; done; echo end test`)
  fmt.Println()

  //Test(nil, 20*time.Second, nil, "", false, "bash", "-c", "cat main.go | grep Test")
}