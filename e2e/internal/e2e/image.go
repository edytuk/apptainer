// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2019-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/apptainer/apptainer/pkg/syfs"
	useragent "github.com/apptainer/apptainer/pkg/util/user-agent"
	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	dockerarchive "github.com/containers/image/v5/docker/archive"
	ociarchive "github.com/containers/image/v5/oci/archive"
	ocilayout "github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
)

var (
	ensureMutex sync.Mutex
	pullMutex   sync.Mutex
)

// EnsureImage checks if e2e test image is already built or builds
// it otherwise.
func EnsureImage(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.ImagePath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.ImagePath,
			err)
	}

	env.RunApptainer(
		t,
		WithProfile(RootProfile),
		WithCommand("build"),
		WithArgs("--force", env.ImagePath, "testdata/Apptainer"),
		ExpectExit(0),
	)
}

// EnsureSingularityImage checks if e2e test singularity image is already
// built or builds it otherwise.
func EnsureSingularityImage(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.SingularityImagePath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.SingularityImagePath,
			err)
	}

	env.RunApptainer(
		t,
		WithProfile(RootProfile),
		WithCommand("build"),
		WithArgs("--force", env.SingularityImagePath, "testdata/Singularity_legacy.def"),
		ExpectExit(0),
	)
}

// EnsureDebianImage checks if the e2e test Debian-based image, with a libc
// that is compatible with the host libc, is already built or builds it
// otherwise.
func EnsureDebianImage(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.DebianImagePath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.DebianImagePath,
			err)
	}

	out, err := exec.Command("ldd", "--version").Output()
	if err != nil {
		t.Fatalf("Error running ldd --version while getting image %q: %+v\n",
			env.DebianImagePath,
			err)
	}
	outstr := string(out)
	end := strings.Index(outstr, "\n")
	if end == -1 {
		t.Fatalf("No newline in ldd output while getting image %q: %+v\n",
			env.DebianImagePath,
			err)
	}
	dot := strings.LastIndex(outstr[0:end], ".")
	if dot == -1 {
		t.Fatalf("No dot in ldd first line while getting image %q: %+v\n",
			env.DebianImagePath,
			err)
	}
	lddversion, err := strconv.Atoi(outstr[dot+1 : end])
	if err != nil {
		t.Fatalf("Could not convert lddversion (%s) to integer while getting image %q: %+v\n",
			outstr[dot+1:end],
			env.DebianImagePath,
			err)
	}
	if lddversion < 17 {
		t.Fatalf("ldd version (%d) not 17 or older while getting image %q: %+v\n",
			lddversion,
			env.DebianImagePath,
			err)
	}

	imageSource := "docker://ubuntu:20.04"
	if lddversion >= 35 {
		imageSource = "docker://ubuntu:22.04"
	}

	env.RunApptainer(
		t,
		// If this is built with the RootProfile, it does not get
		// built with the umoci rootless mode and the container
		// becomes too restricted.
		WithProfile(UserProfile),
		WithCommand("build"),
		WithArgs("--force", env.DebianImagePath, imageSource),
		ExpectExit(0),
	)
}

var orasImageOnce sync.Once

func EnsureORASImage(t *testing.T, env TestEnv) {
	EnsureImage(t, env)

	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	orasImageOnce.Do(func() {
		t.Logf("Pushing %s to %s", env.ImagePath, env.OrasTestImage)
		env.RunApptainer(
			t,
			WithProfile(UserProfile),
			WithCommand("push"),
			WithArgs(env.ImagePath, env.OrasTestImage),
			ExpectExit(0),
		)
		if t.Failed() {
			t.Fatalf("failed to push ORAS image to local registry")
		}
	})
}

// PullImage will pull a test image.
func PullImage(t *testing.T, env TestEnv, imageURL string, arch string, path string) {
	pullMutex.Lock()
	defer pullMutex.Unlock()

	if arch == "" {
		arch = runtime.GOARCH
	}

	switch _, err := os.Stat(path); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n", path, err)
	}

	env.RunApptainer(
		t,
		WithProfile(UserProfile),
		WithCommand("pull"),
		WithArgs("--force", "--allow-unsigned", "--arch", arch, path, imageURL),
		ExpectExit(0),
	)
}

func CopyImage(t *testing.T, source, dest string, insecureSource, insecureDest bool) {
	policy := &signature.Policy{Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()}}
	policyCtx, err := signature.NewPolicyContext(policy)
	if err != nil {
		t.Fatalf("failed to copy %s to %s: %s", source, dest, err)
	}

	srcCtx := &types.SystemContext{
		OCIInsecureSkipTLSVerify:    insecureSource,
		DockerInsecureSkipTLSVerify: types.NewOptionalBool(insecureSource),
		DockerRegistryUserAgent:     useragent.Value(),
	}
	dstCtx := &types.SystemContext{
		OCIInsecureSkipTLSVerify:    insecureDest,
		DockerInsecureSkipTLSVerify: types.NewOptionalBool(insecureDest),
		DockerRegistryUserAgent:     useragent.Value(),
	}

	// Use the auth config written out in dockerhub_auth.go - only if source/dest are not insecure.
	// We don't want to inadvertently send out credentials over http (!)
	u := CurrentUser(t)
	configPath := filepath.Join(u.Dir, ".apptainer", syfs.DockerConfFile)
	if !insecureSource {
		srcCtx.AuthFilePath = configPath
	}
	if !insecureDest {
		dstCtx.AuthFilePath = configPath
	}

	srcRef, err := parseRef(source)
	if err != nil {
		t.Fatalf("failed to parse %s reference: %s", source, err)
	}
	dstRef, err := parseRef(dest)
	if err != nil {
		t.Fatalf("failed to parse %s reference: %s", dest, err)
	}

	_, err = copy.Image(context.Background(), policyCtx, dstRef, srcRef, &copy.Options{
		ReportWriter:   io.Discard,
		SourceCtx:      srcCtx,
		DestinationCtx: dstCtx,
	})
	if err != nil {
		t.Fatalf("failed to copy %s to %s: %s", source, dest, err)
	}
}

// BusyboxImage will provide the path to a local busybox SIF image for the current architecture
func BusyboxSIF(t *testing.T) string {
	busyboxSIF := "testdata/busybox_" + runtime.GOARCH + ".sif"
	_, err := os.Stat(busyboxSIF)
	if os.IsNotExist(err) {
		t.Fatalf("busybox image not found for %s", runtime.GOARCH)
	}
	if err != nil {
		t.Error(err)
	}
	return busyboxSIF
}

var orasOCISIFOnce sync.Once

func EnsureORASOCISIF(t *testing.T, env TestEnv) {
	EnsureOCISIF(t, env)

	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	orasOCISIFOnce.Do(func() {
		t.Logf("Pushing %s to %s", env.OCISIFPath, env.OrasTestOCISIF)
		env.RunApptainer(
			t,
			WithProfile(UserProfile),
			WithCommand("push"),
			WithArgs(env.OCISIFPath, env.OrasTestOCISIF),
			ExpectExit(0),
		)
		if t.Failed() {
			t.Fatalf("failed to push ORAS oci-sif image to local registry")
		}
	})
}

var registryOCISIFOnce sync.Once

func EnsureRegistryOCISIF(t *testing.T, env TestEnv) {
	EnsureOCISIF(t, env)

	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	registryOCISIFOnce.Do(func() {
		t.Logf("Pushing %s to %s", env.OCISIFPath, env.TestRegistryOCISIF)
		env.RunApptainer(
			t,
			WithProfile(UserProfile),
			WithCommand("push"),
			WithArgs(env.OCISIFPath, env.TestRegistryOCISIF),
			ExpectExit(0),
		)
		if t.Failed() {
			t.Fatalf("failed to push oci-sif image to local registry %q", env.TestRegistryOCISIF)
		}
	})
}

func DownloadFile(url string, path string) error {
	dl, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dl.Close()

	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	_, err = io.Copy(dl, r.Body)
	if err != nil {
		return err
	}
	return nil
}

// EnsureImage checks if e2e OCI test archive is available, and fetches
// it otherwise.
func EnsureOCIArchive(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.OCIArchivePath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.OCIArchivePath,
			err)
	}

	// Prepare oci-archive source
	t.Logf("Copying %s to %s", env.TestRegistryImage, "oci-archive:"+env.OCIArchivePath)
	CopyImage(t, env.TestRegistryImage, "oci-archive:"+env.OCIArchivePath, true, false)
}

// EnsureImage checks if e2e OCI-SIF file is available, and fetches it
// otherwise.
func EnsureOCISIF(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.OCISIFPath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.OCISIFPath,
			err)
	}

	env.RunApptainer(
		t,
		WithProfile(UserProfile),
		WithCommand("pull"),
		WithArgs("--oci", "--no-https", env.OCISIFPath, env.TestRegistryImage),
		ExpectExit(0),
	)
}

// EnsureDockerArchive checks if e2e Docker test archive is available, and fetches
// it otherwise.
func EnsureDockerArchive(t *testing.T, env TestEnv) {
	ensureMutex.Lock()
	defer ensureMutex.Unlock()

	switch _, err := os.Stat(env.DockerArchivePath); {
	case err == nil:
		// OK: file exists, return
		return

	case os.IsNotExist(err):
		// OK: file does not exist, continue

	default:
		// FATAL: something else is wrong
		t.Fatalf("Failed when checking image %q: %+v\n",
			env.DockerArchivePath,
			err)
	}

	// Prepare oci-archive source
	t.Logf("Copying %s to %s", env.TestRegistryImage, "docker-archive:"+env.DockerArchivePath)
	CopyImage(t, env.TestRegistryImage, "docker-archive:"+env.DockerArchivePath, true, false)
}

func parseRef(refString string) (ref types.ImageReference, err error) {
	parts := strings.SplitN(refString, ":", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("could not parse image ref: %s", refString)
	}

	switch parts[0] {
	case "docker":
		ref, err = docker.ParseReference(parts[1])
	case "docker-archive":
		ref, err = dockerarchive.ParseReference(parts[1])
	case "oci":
		ref, err = ocilayout.ParseReference(parts[1])
	case "oci-archive":
		ref, err = ociarchive.ParseReference(parts[1])
	default:
		return nil, fmt.Errorf("cannot create an OCI container from %s source", parts[0])
	}

	return ref, err
}
