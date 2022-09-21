// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2018-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

//go:build sylog

package sylog

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/apptainer/apptainer/internal/pkg/test"
)

var defaultWriter = logWriter

func TestPrefix(t *testing.T) {
	test.DropPrivilege(t)
	defer test.ResetPrivilege(t)

	funcName := "goexit()"
	// UID / GID prefix in Debug mode
	uid := os.Geteuid()
	pid := os.Getpid()
	uidStr := fmt.Sprintf("[U=%d,P=%d]", uid, pid)

	tests := []struct {
		name     string
		lvl      messageLevel
		msgColor string
		levelStr string
	}{
		{
			name:     "invalid",
			lvl:      messageLevel(FatalLevel - 1),
			msgColor: "",
			levelStr: "????",
		},
		{
			name:     "fatal",
			lvl:      FatalLevel,
			msgColor: "\x1b[31m",
			levelStr: "FATAL",
		},
		{
			name:     "error",
			lvl:      ErrorLevel,
			msgColor: "\x1b[31m",
			levelStr: "ERROR",
		},
		{
			name:     "warn",
			lvl:      WarnLevel,
			msgColor: "\x1b[33m",
			levelStr: "WARNING",
		},
		{
			name:     "info",
			lvl:      InfoLevel,
			msgColor: "\x1b[34m",
			levelStr: "INFO",
		},
		{
			name:     "debug",
			lvl:      DebugLevel,
			msgColor: "",
			levelStr: "DEBUG",
		},
	}

	// With color
	for _, tt := range tests {
		t.Run("color_"+tt.name, func(t *testing.T) {
			SetLevel(int(tt.lvl), true) // This impacts the output format
			p := prefix(getLoggerLevel(), tt.lvl)
			colorReset := ""
			if tt.msgColor != "" {
				colorReset = "\x1b[0m"
			}
			expectedOutput := fmt.Sprintf("%s%-8s%s ", tt.msgColor, tt.levelStr+":", colorReset)
			if tt.lvl == DebugLevel {
				expectedOutput = fmt.Sprintf("%s%-8s%s%-19s%-30s", tt.msgColor, tt.lvl, colorReset, uidStr, funcName)
			}
			if p != expectedOutput {
				t.Fatalf("test returned %s. instead of %s.", p, expectedOutput)
			}
		})
	}

	// Without color
	for _, tt := range tests {
		t.Run("nocolor_"+tt.name, func(t *testing.T) {
			SetLevel(int(tt.lvl), false) // This impacts the output format
			p := prefix(getLoggerLevel(), tt.lvl)
			expectedOutput := fmt.Sprintf("%-8s ", tt.levelStr+":")
			// invalid cases do *not* support disabling color
			if tt.name == "invalid" {
				expectedOutput = fmt.Sprintf("%-8s ", tt.levelStr+":")
			}
			// debug is special too and does not support disabling color
			if tt.lvl == DebugLevel {
				expectedOutput = fmt.Sprintf("%-8s%-19s%-30s", tt.lvl, uidStr, funcName)
			}
			if p != expectedOutput {
				t.Fatalf("test returned %s. instead of %s.", p, expectedOutput)
			}
		})
	}
}

func TestWriter(t *testing.T) {
	test.DropPrivilege(t)
	defer test.ResetPrivilege(t)

	tests := []struct {
		name           string
		loggerLevel    int
		expectedResult io.Writer
	}{
		{
			name:           "undefined level",
			loggerLevel:    int(FatalLevel - 1),
			expectedResult: ioutil.Discard,
		},
		{
			name:           "no logger",
			loggerLevel:    0,
			expectedResult: os.Stderr,
		},
		{
			name:           "valid logger",
			loggerLevel:    1,
			expectedResult: os.Stderr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetLevel(tt.loggerLevel, true)
			w := Writer()
			if w != tt.expectedResult {
				if w == ioutil.Discard {
					fmt.Printf("%s returned ioutil.Discard\n", tt.name)
				}
				if w == os.Stderr {
					fmt.Printf("%s returned os.Stderr\n", tt.name)
				}
				t.Fatal("Writer() did not return the expected io.Writer")
			}
		})
	}
}

func TestWritef(t *testing.T) {
	const str = "just a test"

	var buf bytes.Buffer
	logWriter = &buf

	defer func() {
		logWriter = defaultWriter
	}()

	tests := []struct {
		name string
		lvl  messageLevel
	}{
		{
			name: "info",
			lvl:  InfoLevel,
		},
		{
			name: "error",
			lvl:  ErrorLevel,
		},
		{
			name: "warning",
			lvl:  WarnLevel,
		},
		{
			name: "fatal",
			lvl:  FatalLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetLevel(int(tt.lvl), false)
			buf.Reset()

			writef(tt.lvl, "%s", str)
			expectedResult := prefix(getLoggerLevel(), tt.lvl) + str + "\n"
			if buf.String() != expectedResult {
				t.Fatalf("test %s returned %s instead of %s", tt.name, buf.String(), expectedResult)
			}
		})
	}

	// corner case
	SetLevel(int(FatalLevel), true)
	expectedResult := ""
	buf.Reset()
	writef(InfoLevel, "%s", str)
	if buf.String() != expectedResult {
		t.Fatalf("test returned %s instead of an empty string", buf.String())
	}
}

func TestGetLevel(t *testing.T) {
	tests := []struct {
		name           string
		lvl            messageLevel
		expectedResult int
	}{
		{
			name:           "fatal",
			lvl:            FatalLevel,
			expectedResult: -4,
		},
		{
			name:           "error",
			lvl:            ErrorLevel,
			expectedResult: -3,
		},
		{
			name:           "warn",
			lvl:            WarnLevel,
			expectedResult: -2,
		},
		{
			name:           "info",
			lvl:            InfoLevel,
			expectedResult: 1,
		},
		{
			name:           "verbose",
			lvl:            VerboseLevel,
			expectedResult: 2,
		},
		{
			name:           "verbose2",
			lvl:            Verbose2Level,
			expectedResult: 3,
		},
		{
			name:           "verbose3",
			lvl:            Verbose3Level,
			expectedResult: 4,
		},
		{
			name:           "debug",
			lvl:            DebugLevel,
			expectedResult: 5,
		},
		{
			name:           "invalid",
			lvl:            messageLevel(-10),
			expectedResult: -4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetLevel(int(tt.lvl), true)
			lvl := GetLevel()
			if lvl != int(tt.lvl) {
				t.Fatalf("test %s was expected to return %d but returned %d instead", tt.name, tt.expectedResult, lvl)
			}
		})
	}
}

func TestGetenv(t *testing.T) {
	str := GetEnvVar()
	expectedResult := "APPTAINER_MESSAGELEVEL="
	if str[:len(expectedResult)] != expectedResult {
		t.Fatalf("Test returned %s instead of %s", str[:len(expectedResult)], expectedResult)
	}
}

const testStr = "test message"

type fnOut func(format string, a ...interface{})

func runTestLogFn(t *testing.T, errFd *os.File, fn fnOut) {
	if errFd != nil {
		fn("%s", testStr)
		return
	}

	SetLevel(int(DebugLevel), false)

	var buf bytes.Buffer
	logWriter = &buf

	fn("%s\n", testStr)

	logWriter = defaultWriter

	out := buf.String()

	// We check the formatting of the output we caught
	regExpClass := regexp.MustCompile(`^(.*) \[U=`)
	classResult := regExpClass.FindStringSubmatch(out)

	if len(classResult) < 2 {
		t.Fatalf("unexpected format: %s", out)
	}
	class := classResult[1]
	class = strings.Trim(class, " \t")
	if class != "WARNING" && class != "INFO" && class != "DEBUG" && class != "VERBOSE" {
		t.Fatalf("failed to recognize the type of message: %s.", class)
	}

	regExpMsg := regexp.MustCompile(`runTestLogFn\(\)(.*)\n`)
	msgResult := regExpMsg.FindStringSubmatch(out)
	if len(msgResult) < 2 {
		t.Fatalf("unexpected format: %s", out)
	}
	msg := msgResult[1]
	if msg[len(msg)-len(testStr):] != testStr {
		t.Fatalf("invalid test message: %s vs. %s", msg[len(msg)-len(testStr):], testStr)
	}
}

func TestStderrOutput(t *testing.T) {
	tests := []struct {
		name string
		out  *os.File
	}{
		{
			// We just call a few funtions that output to stderr, not much we can test
			// except make sure that whatever potential modification to the code does
			// not make the code crash
			name: "default Stderr",
			out:  os.Stderr,
		},
		{
			name: "pipe",
			out:  nil, // Since nil, the code will create a bytes buffer for that case so we can catch what is written via the buffer
		},
	}

	// reset logger level altered by previous tests
	SetLevel(0, true)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTestLogFn(t, tt.out, Warningf)
			runTestLogFn(t, tt.out, Infof)
			runTestLogFn(t, tt.out, Verbosef)
			runTestLogFn(t, tt.out, Debugf)
		})
	}
}
