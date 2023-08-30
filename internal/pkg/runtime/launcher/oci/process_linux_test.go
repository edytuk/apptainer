// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022-2023, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package oci

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/apptainer/apptainer/internal/pkg/runtime/engine/config/oci"
	"github.com/apptainer/apptainer/internal/pkg/runtime/launcher"
	"github.com/apptainer/apptainer/pkg/util/capabilities"
	imgspecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/samber/lo"
)

func TestApptainerEnvMap(t *testing.T) {
	tests := []struct {
		name   string
		setEnv map[string]string
		want   map[string]string
	}{
		{
			name:   "None",
			setEnv: map[string]string{},
			want:   map[string]string{},
		},
		{
			name:   "NonPrefixed",
			setEnv: map[string]string{"FOO": "bar"},
			want:   map[string]string{},
		},
		{
			name:   "PrefixedSingle",
			setEnv: map[string]string{"APPTAINERENV_FOO": "bar"},
			want:   map[string]string{"FOO": "bar"},
		},
		{
			name: "PrefixedMultiple",
			setEnv: map[string]string{
				"APPTAINERENV_FOO": "bar",
				"APPTAINERENV_ABC": "123",
			},
			want: map[string]string{
				"FOO": "bar",
				"ABC": "123",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.setEnv {
				os.Setenv(k, v)
				t.Cleanup(func() {
					os.Unsetenv(k)
				})
			}
			if got := apptainerEnvMap(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("apptainerEnvMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvFileMap(t *testing.T) {
	tests := []struct {
		name    string
		envFile string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "EmptyFile",
			envFile: "",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name: "Simple",
			envFile: `FOO=BAR
			ABC=123`,
			want: map[string]string{
				"FOO": "BAR",
				"ABC": "123",
			},
			wantErr: false,
		},
		{
			name:    "DoubleQuote",
			envFile: `FOO="FOO BAR"`,
			want: map[string]string{
				"FOO": "FOO BAR",
			},
			wantErr: false,
		},
		{
			name:    "SingleQuote",
			envFile: `FOO='FOO BAR'`,
			want: map[string]string{
				"FOO": "FOO BAR",
			},
			wantErr: false,
		},
		{
			name:    "MultiLine",
			envFile: "FOO=\"FOO\nBAR\"",
			want: map[string]string{
				"FOO": "FOO\nBAR",
			},
			wantErr: false,
		},
		{
			name:    "Invalid",
			envFile: "!!!@@NOTAVAR",
			want:    map[string]string{},
			wantErr: true,
		},
	}

	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "env-file")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(envFile, []byte(tt.envFile), 0o755); err != nil {
				t.Fatalf("Could not write test env-file: %v", err)
			}

			got, err := envFileMap(context.Background(), envFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("envFileMap() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("envFileMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetProcessArgs(t *testing.T) {
	tests := []struct {
		name              string
		nativeSIF         bool
		imgEntrypoint     []string
		imgCmd            []string
		bundleProcess     string
		bundleArgs        []string
		expectProcessArgs []string
	}{
		{
			name:              "imageEntrypointOnly",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{},
			bundleProcess:     "",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"ENTRYPOINT"},
		},
		{
			name:              "imageCmdOnly",
			imgEntrypoint:     []string{},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"CMD"},
		},
		{
			name:              "imageEntrypointCMD",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"ENTRYPOINT", "CMD"},
		},
		{
			name:              "ProcessOnly",
			imgEntrypoint:     []string{},
			imgCmd:            []string{},
			bundleProcess:     "PROCESS",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"PROCESS"},
		},
		{
			name:              "ArgsOnly",
			imgEntrypoint:     []string{},
			imgCmd:            []string{},
			bundleProcess:     "",
			bundleArgs:        []string{"ARGS"},
			expectProcessArgs: []string{"ARGS"},
		},
		{
			name:              "ProcessArgs",
			imgEntrypoint:     []string{},
			imgCmd:            []string{},
			bundleProcess:     "PROCESS",
			bundleArgs:        []string{"ARGS"},
			expectProcessArgs: []string{"PROCESS", "ARGS"},
		},
		{
			name:              "overrideEntrypointOnlyProcess",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{},
			bundleProcess:     "PROCESS",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"PROCESS"},
		},
		{
			name:              "overrideCmdOnlyArgs",
			imgEntrypoint:     []string{},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "",
			bundleArgs:        []string{"ARGS"},
			expectProcessArgs: []string{"ARGS"},
		},
		{
			name:              "overrideBothProcess",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "PROCESS",
			bundleArgs:        []string{},
			expectProcessArgs: []string{"PROCESS"},
		},
		{
			name:              "overrideBothArgs",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "",
			bundleArgs:        []string{"ARGS"},
			expectProcessArgs: []string{"ENTRYPOINT", "ARGS"},
		},
		{
			name:              "overrideBothProcessArgs",
			imgEntrypoint:     []string{"ENTRYPOINT"},
			imgCmd:            []string{"CMD"},
			bundleProcess:     "PROCESS",
			bundleArgs:        []string{"ARGS"},
			expectProcessArgs: []string{"PROCESS", "ARGS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := imgspecv1.Image{
				Config: imgspecv1.ImageConfig{
					Entrypoint: tt.imgEntrypoint,
					Cmd:        tt.imgCmd,
				},
			}
			ep := launcher.ExecParams{
				Process: tt.bundleProcess,
				Args:    tt.bundleArgs,
			}
			args := getProcessArgs(i, ep)
			if !reflect.DeepEqual(args, tt.expectProcessArgs) {
				t.Errorf("Expected: %v, Got: %v", tt.expectProcessArgs, args)
			}
		})
	}
}

func TestGetProcessEnv(t *testing.T) {
	tests := []struct {
		name      string
		imageEnv  []string
		bundleEnv map[string]string
		wantEnv   []string
	}{
		{
			name:      "Default",
			imageEnv:  []string{},
			bundleEnv: map[string]string{},
			wantEnv:   []string{"LD_LIBRARY_PATH=/.singularity.d/libs"},
		},
		{
			name:      "ImagePath",
			imageEnv:  []string{"PATH=/foo"},
			bundleEnv: map[string]string{},
			wantEnv: []string{
				"PATH=/foo",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "OverridePath",
			imageEnv:  []string{"PATH=/foo"},
			bundleEnv: map[string]string{"PATH": "/bar"},
			wantEnv: []string{
				"PATH=/bar",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "AppendPath",
			imageEnv:  []string{"PATH=/foo"},
			bundleEnv: map[string]string{"APPEND_PATH": "/bar"},
			wantEnv: []string{
				"PATH=/foo:/bar",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "PrependPath",
			imageEnv:  []string{"PATH=/foo"},
			bundleEnv: map[string]string{"PREPEND_PATH": "/bar"},
			wantEnv: []string{
				"PATH=/bar:/foo",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "ImageLdLibraryPath",
			imageEnv:  []string{"LD_LIBRARY_PATH=/foo"},
			bundleEnv: map[string]string{},
			wantEnv: []string{
				"LD_LIBRARY_PATH=/foo:/.singularity.d/libs",
			},
		},
		{
			name:      "BundleLdLibraryPath",
			imageEnv:  []string{},
			bundleEnv: map[string]string{"LD_LIBRARY_PATH": "/foo"},
			wantEnv: []string{
				"LD_LIBRARY_PATH=/foo:/.singularity.d/libs",
			},
		},
		{
			name:      "OverrideLdLibraryPath",
			imageEnv:  []string{"LD_LIBRARY_PATH=/foo"},
			bundleEnv: map[string]string{"LD_LIBRARY_PATH": "/bar"},
			wantEnv: []string{
				"LD_LIBRARY_PATH=/bar:/.singularity.d/libs",
			},
		},
		{
			name:      "ImageVar",
			imageEnv:  []string{"FOO=bar"},
			bundleEnv: map[string]string{},
			wantEnv: []string{
				"FOO=bar",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "ImageOverride",
			imageEnv:  []string{"FOO=bar"},
			bundleEnv: map[string]string{"FOO": "baz"},
			wantEnv: []string{
				"FOO=baz",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
		{
			name:      "ImageAdditional",
			imageEnv:  []string{"FOO=bar"},
			bundleEnv: map[string]string{"ABC": "123"},
			wantEnv: []string{
				"FOO=bar",
				"ABC=123",
				"LD_LIBRARY_PATH=/.singularity.d/libs",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imgSpec := imgspecv1.Image{
				Config: imgspecv1.ImageConfig{Env: tt.imageEnv},
			}

			env := getProcessEnv(imgSpec, tt.bundleEnv)

			if !reflect.DeepEqual(env, tt.wantEnv) {
				t.Errorf("want: %v, got: %v", tt.wantEnv, env)
			}
		})
	}
}

func TestLauncher_reverseMapByRange(t *testing.T) {
	tests := []struct {
		name       string
		targetUID  uint32
		targetGID  uint32
		subUIDMap  specs.LinuxIDMapping
		subGIDMap  specs.LinuxIDMapping
		wantUIDMap []specs.LinuxIDMapping
		wantGIDMap []specs.LinuxIDMapping
		wantErr    bool
	}{
		{
			// TargetID is smaller than size of subuid/subgid map.
			name:      "LowTargetID",
			targetUID: 1000,
			targetGID: 2000,
			subUIDMap: specs.LinuxIDMapping{HostID: 1000, ContainerID: 100000, Size: 65536},
			subGIDMap: specs.LinuxIDMapping{HostID: 2000, ContainerID: 200000, Size: 65536},
			wantUIDMap: []specs.LinuxIDMapping{
				{ContainerID: 0, HostID: 1, Size: 1000},
				{ContainerID: 1000, HostID: 0, Size: 1},
				{ContainerID: 1001, HostID: 1001, Size: 64536},
			},
			wantGIDMap: []specs.LinuxIDMapping{
				{ContainerID: 0, HostID: 1, Size: 2000},
				{ContainerID: 2000, HostID: 0, Size: 1},
				{ContainerID: 2001, HostID: 2001, Size: 63536},
			},
		},
		{
			// TargetID is higher than size of subuid/subgid map.
			name:      "HighTargetID",
			targetUID: 70000,
			targetGID: 80000,
			subUIDMap: specs.LinuxIDMapping{HostID: 1000, ContainerID: 100000, Size: 65536},
			subGIDMap: specs.LinuxIDMapping{HostID: 2000, ContainerID: 200000, Size: 65536},
			wantUIDMap: []specs.LinuxIDMapping{
				{ContainerID: 0, HostID: 1, Size: 65536},
				{ContainerID: 70000, HostID: 0, Size: 1},
			},
			wantGIDMap: []specs.LinuxIDMapping{
				{ContainerID: 0, HostID: 1, Size: 65536},
				{ContainerID: 80000, HostID: 0, Size: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUIDMap, gotGIDMap := reverseMapByRange(tt.targetUID, tt.targetGID, tt.subUIDMap, tt.subGIDMap)
			if !reflect.DeepEqual(gotUIDMap, tt.wantUIDMap) {
				t.Errorf("Launcher.getReverseUserMaps() gotUidMap = %v, want %v", gotUIDMap, tt.wantUIDMap)
			}
			if !reflect.DeepEqual(gotGIDMap, tt.wantGIDMap) {
				t.Errorf("Launcher.getReverseUserMaps() gotGidMap = %v, want %v", gotGIDMap, tt.wantGIDMap)
			}
		})
	}
}

func TestLauncher_getBaseCapabilities(t *testing.T) {
	currCaps, err := capabilities.GetProcessEffective()
	if err != nil {
		t.Fatal(err)
	}
	currCapStrings := capabilities.ToStrings(currCaps)

	tests := []struct {
		name      string
		keepPrivs bool
		noPrivs   bool
		want      []string
		wantErr   bool
	}{
		{
			name:      "Default",
			keepPrivs: false,
			noPrivs:   false,
			want:      oci.DefaultCaps,
			wantErr:   false,
		},
		{
			name:      "NoPrivs",
			keepPrivs: false,
			noPrivs:   true,
			want:      []string{},
			wantErr:   false,
		},
		{
			name:      "NoPrivsPrecendence",
			keepPrivs: true,
			noPrivs:   true,
			want:      []string{},
			wantErr:   false,
		},
		{
			name:      "KeepPrivs",
			keepPrivs: true,
			noPrivs:   false,
			want:      currCapStrings,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Launcher{
				cfg: launcher.Options{
					KeepPrivs: tt.keepPrivs,
					NoPrivs:   tt.noPrivs,
				},
			}
			got, err := l.getBaseCapabilities()
			if (err != nil) != tt.wantErr {
				t.Errorf("Launcher.getBaseCapabilities() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Launcher.getBaseCapabilities() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLauncher_getProcessCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		addCaps  string
		dropCaps string
		uid      uint32
		want     *specs.LinuxCapabilities
		wantErr  bool
	}{
		{
			name:     "DefaultRoot",
			addCaps:  "",
			dropCaps: "",
			uid:      0,
			want: &specs.LinuxCapabilities{
				Permitted:   oci.DefaultCaps,
				Effective:   oci.DefaultCaps,
				Bounding:    oci.DefaultCaps,
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "DefaultUser",
			addCaps:  "",
			dropCaps: "",
			uid:      1000,
			want: &specs.LinuxCapabilities{
				Permitted:   []string{},
				Effective:   []string{},
				Bounding:    oci.DefaultCaps,
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "AddRoot",
			addCaps:  "CAP_SYSLOG,CAP_WAKE_ALARM",
			dropCaps: "",
			uid:      0,
			want: &specs.LinuxCapabilities{
				Permitted:   append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"),
				Effective:   append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"),
				Bounding:    append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"),
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "DropRoot",
			addCaps:  "",
			dropCaps: "CAP_SETUID,CAP_SETGID",
			uid:      0,
			want: &specs.LinuxCapabilities{
				Permitted:   lo.Without(oci.DefaultCaps, "CAP_SETUID", "CAP_SETGID"),
				Effective:   lo.Without(oci.DefaultCaps, "CAP_SETUID", "CAP_SETGID"),
				Bounding:    lo.Without(oci.DefaultCaps, "CAP_SETUID", "CAP_SETGID"),
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "AddUser",
			addCaps:  "CAP_SYSLOG,CAP_WAKE_ALARM",
			dropCaps: "",
			uid:      1000,
			want: &specs.LinuxCapabilities{
				Permitted:   []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Effective:   []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Bounding:    append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"),
				Inheritable: []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Ambient:     []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
			},
			wantErr: false,
		},
		{
			name:     "DropUser",
			addCaps:  "",
			dropCaps: "CAP_SETUID,CAP_SETGID",
			uid:      1000,
			want: &specs.LinuxCapabilities{
				Permitted:   []string{},
				Effective:   []string{},
				Bounding:    lo.Without(oci.DefaultCaps, "CAP_SETUID", "CAP_SETGID"),
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "AddDropRoot",
			addCaps:  "CAP_SYSLOG,CAP_WAKE_ALARM",
			dropCaps: "CAP_SETUID,CAP_SETGID",
			uid:      0,
			want: &specs.LinuxCapabilities{
				Permitted:   lo.Without(append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"), "CAP_SETUID", "CAP_SETGID"),
				Effective:   lo.Without(append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"), "CAP_SETUID", "CAP_SETGID"),
				Bounding:    lo.Without(append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"), "CAP_SETUID", "CAP_SETGID"),
				Inheritable: []string{},
				Ambient:     []string{},
			},
			wantErr: false,
		},
		{
			name:     "AddDropUser",
			addCaps:  "CAP_SYSLOG,CAP_WAKE_ALARM",
			dropCaps: "CAP_SETUID,CAP_SETGID",
			uid:      1000,
			want: &specs.LinuxCapabilities{
				Permitted:   []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Effective:   []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Bounding:    lo.Without(append(oci.DefaultCaps, "CAP_SYSLOG", "CAP_WAKE_ALARM"), "CAP_SETUID", "CAP_SETGID"),
				Inheritable: []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
				Ambient:     []string{"CAP_SYSLOG", "CAP_WAKE_ALARM"},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Launcher{
				cfg: launcher.Options{
					AddCaps:  tt.addCaps,
					DropCaps: tt.dropCaps,
				},
			}
			got, err := l.getProcessCapabilities(tt.uid)
			if (err != nil) != tt.wantErr {
				t.Errorf("Launcher.getProcessCapabilities() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Launcher.getProcessCapabilities() = %v, want %v", got, tt.want)
			}
		})
	}
}
