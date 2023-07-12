// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022-2023, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Package oci implements a Launcher that will configure and launch a container
// with an OCI runtime. It also provides implementations of OCI state
// transitions that can be called directly, Create/Start/Kill etc.
package oci

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/apptainer/apptainer/internal/pkg/runtime/launcher"
	"github.com/apptainer/apptainer/pkg/util/apptainerconf"
	"github.com/apptainer/apptainer/pkg/util/bind"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func Test_addBindMount(t *testing.T) {
	tests := []struct {
		name       string
		b          bind.Path
		wantMounts *[]specs.Mount
		wantErr    bool
	}{
		{
			name: "Valid",
			b: bind.Path{
				Source:      "/tmp",
				Destination: "/tmp",
			},
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/tmp",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev"},
				},
			},
		},
		{
			name: "ValidRO",
			b: bind.Path{
				Source:      "/tmp",
				Destination: "/tmp",
				Options:     map[string]*bind.Option{"ro": {}},
			},
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/tmp",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
			},
		},
		{
			name: "BadSource",
			b: bind.Path{
				Source:      "doesnotexist!",
				Destination: "/mnt",
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "RelDest",
			b: bind.Path{
				Source:      "/tmp",
				Destination: "relative",
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "ImageID",
			b: bind.Path{
				Source:      "/myimage.sif",
				Destination: "/mnt",
				Options:     map[string]*bind.Option{"id": {Value: "4"}},
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "ImageSrc",
			b: bind.Path{
				Source:      "/myimage.sif",
				Destination: "/mnt",
				Options:     map[string]*bind.Option{"img-src": {Value: "/test"}},
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mounts := &[]specs.Mount{}
			err := addBindMount(mounts, tt.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("addBindMount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(mounts, tt.wantMounts) {
				t.Errorf("addBindMount() want %v, got %v", tt.wantMounts, mounts)
			}
		})
	}
}

func TestLauncher_addBindMounts(t *testing.T) {
	tests := []struct {
		name       string
		cfg        launcher.Options
		userbind   bool
		wantMounts *[]specs.Mount
		wantErr    bool
	}{
		{
			name: "Disabled",
			cfg: launcher.Options{
				BindPaths: []string{"/tmp"},
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    false,
		},
		{
			name: "ValidBindSrc",
			cfg: launcher.Options{
				BindPaths: []string{"/tmp"},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/tmp",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev"},
				},
			},
			wantErr: false,
		},
		{
			name: "ValidBindSrcDst",
			cfg: launcher.Options{
				BindPaths: []string{"/tmp:/mnt"},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/mnt",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev"},
				},
			},
			wantErr: false,
		},
		{
			name: "ValidBindRO",
			cfg: launcher.Options{
				BindPaths: []string{"/tmp:/mnt:ro"},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/mnt",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
			},
			wantErr: false,
		},
		{
			name: "InvalidBindSrc",
			cfg: launcher.Options{
				BindPaths: []string{"!doesnotexist"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "RelBindDst",
			cfg: launcher.Options{
				BindPaths: []string{"/tmp:relative"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "UnsupportedBindID",
			cfg: launcher.Options{
				BindPaths: []string{"my.sif:/mnt:id=2"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "UnsupportedBindImgSrc",
			cfg: launcher.Options{
				BindPaths: []string{"my.sif:/mnt:img-src=/test"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "ValidMount",
			cfg: launcher.Options{
				Mounts: []string{"type=bind,source=/tmp,destination=/mnt"},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/mnt",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev"},
				},
			},
			wantErr: false,
		},
		{
			name: "ValidMountRO",
			cfg: launcher.Options{
				Mounts: []string{"type=bind,source=/tmp,destination=/mnt,ro"},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      "/tmp",
					Destination: "/mnt",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
			},
			wantErr: false,
		},
		{
			name: "UnsupportedMountID",
			cfg: launcher.Options{
				Mounts: []string{"type=bind,source=my.sif,destination=/mnt,id=2"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "UnsupportedMountImgSrc",
			cfg: launcher.Options{
				Mounts: []string{"type=bind,source=my.sif,destination=/mnt,image-src=/test"},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Launcher{
				cfg:           tt.cfg,
				apptainerConf: &apptainerconf.File{},
			}
			if tt.userbind {
				l.apptainerConf.UserBindControl = true
			}
			mounts := &[]specs.Mount{}
			err := l.addBindMounts(mounts)
			if (err != nil) != tt.wantErr {
				t.Errorf("addBindMount() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(mounts, tt.wantMounts) {
				t.Errorf("addBindMount() want %v, got %v", tt.wantMounts, mounts)
			}
		})
	}
}

func TestLauncher_addLibrariesMounts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "add-libraries-mounts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			os.RemoveAll(tmpDir)
		}
	})

	lib1 := filepath.Join(tmpDir, "lib1.so")
	lib2 := filepath.Join(tmpDir, "lib2.so")
	libInvalid := filepath.Join(tmpDir, "invalid")
	if err := os.WriteFile(lib1, []byte("lib1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lib2, []byte("lib2"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		cfg        launcher.Options
		userbind   bool
		wantMounts *[]specs.Mount
		wantErr    bool
	}{
		{
			name: "Disabled",
			cfg: launcher.Options{
				ContainLibs: []string{lib1},
			},
			wantMounts: &[]specs.Mount{},
			wantErr:    false,
		},
		{
			name: "Invalid",
			cfg: launcher.Options{
				ContainLibs: []string{libInvalid},
			},
			userbind:   true,
			wantMounts: &[]specs.Mount{},
			wantErr:    true,
		},
		{
			name: "Single",
			cfg: launcher.Options{
				ContainLibs: []string{lib1},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      lib1,
					Destination: "/.singularity.d/libs/lib1.so",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
			},
			wantErr: false,
		},
		{
			name: "Multiple",
			cfg: launcher.Options{
				ContainLibs: []string{lib1, lib2},
			},
			userbind: true,
			wantMounts: &[]specs.Mount{
				{
					Source:      lib1,
					Destination: "/.singularity.d/libs/lib1.so",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
				{
					Source:      lib2,
					Destination: "/.singularity.d/libs/lib2.so",
					Type:        "none",
					Options:     []string{"rbind", "nosuid", "nodev", "ro"},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Launcher{
				cfg:           tt.cfg,
				apptainerConf: &apptainerconf.File{},
			}
			if tt.userbind {
				l.apptainerConf.UserBindControl = true
			}
			mounts := &[]specs.Mount{}
			err := l.addLibrariesMounts(mounts)
			if (err != nil) != tt.wantErr {
				t.Errorf("addLibrariesMounts() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(mounts, tt.wantMounts) {
				t.Errorf("addLibrariesMounts() want %v, got %v", tt.wantMounts, mounts)
			}
		})
	}
}
