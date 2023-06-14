// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apexlog "github.com/apex/log"
	"github.com/apptainer/apptainer/internal/pkg/build/oci"
	"github.com/apptainer/apptainer/internal/pkg/cache"
	"github.com/apptainer/apptainer/internal/pkg/runtime/engine/config/oci/generate"
	"github.com/apptainer/apptainer/pkg/ocibundle"
	"github.com/apptainer/apptainer/pkg/ocibundle/tools"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	dockerarchive "github.com/containers/image/v5/docker/archive"
	dockerdaemon "github.com/containers/image/v5/docker/daemon"
	ociarchive "github.com/containers/image/v5/oci/archive"
	ocilayout "github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
	imgspecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/umoci"
	umocilayer "github.com/opencontainers/umoci/oci/layer"
	"github.com/opencontainers/umoci/pkg/idtools"
)

const apptainerLibs = "/.singularity.d/libs"

// Bundle is a native OCI bundle, created from imageRef.
type Bundle struct {
	// imageRef is the reference to the OCI image source, e.g. docker://ubuntu:latest.
	imageRef string
	// imageSpec is the OCI image information, CMD, ENTRYPOINT, etc.
	imageSpec *imgspecv1.Image
	// bundlePath is the location where the OCI bundle will be created.
	bundlePath string
	// sysCtx provides containers/image transport configuration (auth etc.)
	sysCtx *types.SystemContext
	// imgCache is a Apptainer image cache, which OCI blobs are pulled through.
	// Note that we only use the 'blob' cache section. The 'oci-tmp' cache section holds
	// OCI->SIF conversions, which are not used here.
	imgCache *cache.Handle
	// process is the command to execute, which may override the image's ENTRYPOINT / CMD.
	process string
	// args are the command arguments, which may override the image's CMD.
	args []string
	// env is the container environment to set, which will be merged with the image's env.
	env map[string]string
	// Generic bundle properties
	ocibundle.Bundle
}

type Option func(b *Bundle) error

// OptBundlePath sets the path that the bundle will be created at.
func OptBundlePath(bp string) Option {
	return func(b *Bundle) error {
		var err error
		b.bundlePath, err = filepath.Abs(bp)
		if err != nil {
			return fmt.Errorf("failed to determine bundle path: %s", err)
		}
		return nil
	}
}

// OptImageRef sets the image source reference, from which the bundle will be created.
func OptImageRef(ref string) Option {
	return func(b *Bundle) error {
		b.imageRef = ref
		return nil
	}
}

// OptSysCtx sets the OCI client SystemContext holding auth information etc.
func OptSysCtx(sc *types.SystemContext) Option {
	return func(b *Bundle) error {
		b.sysCtx = sc
		return nil
	}
}

// OptImgCache sets the Apptainer image cache used to pull through OCI blobs.
func OptImgCache(ic *cache.Handle) Option {
	return func(b *Bundle) error {
		b.imgCache = ic
		return nil
	}
}

// OptProcessArgs sets the command and arguments to run in the container.
func OptProcessArgs(process string, args []string) Option {
	return func(b *Bundle) error {
		b.process = process
		b.args = args
		return nil
	}
}

// OptEnv sets the environment to be set, merged with the image ENV.
func OptProcessEnv(env map[string]string) Option {
	return func(b *Bundle) error {
		b.env = env
		return nil
	}
}

// New returns a bundle interface to create/delete an OCI bundle from an OCI image ref.
func New(opts ...Option) (ocibundle.Bundle, error) {
	b := Bundle{
		imageRef: "",
		sysCtx:   &types.SystemContext{},
		imgCache: nil,
	}

	for _, opt := range opts {
		if err := opt(&b); err != nil {
			return nil, fmt.Errorf("while initializing bundle: %w", err)
		}
	}

	return &b, nil
}

// Delete erases OCI bundle created an OCI image ref
func (b *Bundle) Delete() error {
	return tools.DeleteBundle(b.bundlePath)
}

// Create will created the on-disk structures for the OCI bundle, so that it is ready for execution.
func (b *Bundle) Create(ctx context.Context, ociConfig *specs.Spec) error {
	// generate OCI bundle directory and config
	g, err := tools.GenerateBundleConfig(b.bundlePath, ociConfig)
	if err != nil {
		return fmt.Errorf("failed to generate OCI bundle/config: %s", err)
	}
	// Due to our caching approach for OCI blobs, we need to pull blobs for the image
	// out into a separate oci-layout directory.
	tmpDir, err := os.MkdirTemp("", "oci-tmp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	// Fetch into temp oci layout (will pull through cache if enabled)
	if err := b.fetchImage(ctx, tmpDir); err != nil {
		return err
	}
	// Extract from temp oci layout into bundle rootfs
	if err := b.extractImage(ctx, tmpDir); err != nil {
		return err
	}
	// Remove the temp oci layout.
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	// ProcessArgs are set here, rather than in the launcher spec generation, as we need to
	// consult the image Config to handle combining ENTRYPOINT/CMD with user
	// provided args.
	b.setProcessArgs(g)
	// Ditto for environment handling (merge image and user/rt requested).
	b.setProcessEnv(g)

	return b.writeConfig(g)
}

// Path returns the bundle's path on disk.
func (b *Bundle) Path() string {
	return b.bundlePath
}

func (b *Bundle) setProcessArgs(g *generate.Generator) {
	var processArgs []string

	if b.process != "" {
		processArgs = []string{b.process}
	} else {
		processArgs = b.imageSpec.Config.Entrypoint
	}

	if len(b.args) > 0 {
		processArgs = append(processArgs, b.args...)
	} else {
		if b.process == "" {
			processArgs = append(processArgs, b.imageSpec.Config.Cmd...)
		}
	}

	g.SetProcessArgs(processArgs)
}

// setProcessEnv combines the image config ENV with the ENV requested in the runtime provided spec.
// APPEND_PATH and PREPEND_PATH are honored as with the native apptainer runtime.
// LD_LIBRARY_PATH is modified to always include the apptainer lib bind directory.
func (b *Bundle) setProcessEnv(g *generate.Generator) {
	if g.Config == nil {
		g.Config = &specs.Spec{}
	}
	if g.Config.Process == nil {
		g.Config.Process = &specs.Process{}
	}
	g.Config.Process.Env = b.imageSpec.Config.Env

	path := ""
	appendPath := ""
	prependPath := ""
	ldLibraryPath := ""

	// Obtain PATH, and LD_LIBRARY_PATH if set in the image config.
	for _, env := range b.imageSpec.Config.Env {
		e := strings.SplitN(env, "=", 2)
		if len(e) < 2 {
			continue
		}
		if e[0] == "PATH" {
			path = e[1]
		}
		if e[0] == "LD_LIBRARY_PATH" {
			ldLibraryPath = e[1]
		}
	}

	// Apply env vars from spec, except PATH and LD_LIBRARY_PATH releated.
	for k, v := range b.env {
		switch k {
		case "PATH":
			path = v
		case "APPEND_PATH":
			appendPath = v
		case "PREPEND_PATH":
			prependPath = v
		case "LD_LIBRARY_PATH":
			ldLibraryPath = v
		default:
			g.SetProcessEnv(k, v)
		}
	}

	// Compute and set optionally APPEND-ed / PREPEND-ed PATH.
	if appendPath != "" {
		path = path + ":" + appendPath
	}
	if prependPath != "" {
		path = prependPath + ":" + path
	}
	if path != "" {
		g.SetProcessEnv("PATH", path)
	}

	// Ensure LD_LIBRARY_PATH always contains apptainer lib binding dir.
	if !strings.Contains(ldLibraryPath, apptainerLibs) {
		ldLibraryPath = strings.TrimPrefix(ldLibraryPath+":"+apptainerLibs, ":")
	}
	g.SetProcessEnv("LD_LIBRARY_PATH", ldLibraryPath)
}

func (b *Bundle) writeConfig(g *generate.Generator) error {
	return tools.SaveBundleConfig(b.bundlePath, g)
}

func (b *Bundle) fetchImage(ctx context.Context, tmpDir string) error {
	if b.sysCtx == nil {
		return fmt.Errorf("sysctx must be provided")
	}

	policy := &signature.Policy{Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()}}
	policyCtx, err := signature.NewPolicyContext(policy)
	if err != nil {
		return err
	}

	parts := strings.SplitN(b.imageRef, ":", 2)
	if len(parts) < 2 {
		return fmt.Errorf("could not parse image ref: %s", b.imageRef)
	}
	var srcRef types.ImageReference

	switch parts[0] {
	case "docker":
		srcRef, err = docker.ParseReference(parts[1])
	case "docker-archive":
		srcRef, err = dockerarchive.ParseReference(parts[1])
	case "docker-daemon":
		srcRef, err = dockerdaemon.ParseReference(parts[1])
	case "oci":
		srcRef, err = ocilayout.ParseReference(parts[1])
	case "oci-archive":
		srcRef, err = ociarchive.ParseReference(parts[1])
	default:
		return fmt.Errorf("cannot create an OCI container from %s source", parts[0])
	}

	if err != nil {
		return fmt.Errorf("invalid image source: %w", err)
	}

	if b.imgCache != nil {
		// Grab the modified source ref from the cache
		srcRef, err = oci.ConvertReference(ctx, b.imgCache, srcRef, b.sysCtx)
		if err != nil {
			return err
		}
	}

	tmpfsRef, err := ocilayout.ParseReference(tmpDir + ":" + "tmp")
	if err != nil {
		return err
	}

	_, err = copy.Image(ctx, policyCtx, tmpfsRef, srcRef, &copy.Options{
		ReportWriter: sylog.Writer(),
		SourceCtx:    b.sysCtx,
	})
	if err != nil {
		return err
	}

	img, err := srcRef.NewImage(ctx, b.sysCtx)
	if err != nil {
		return err
	}
	defer img.Close()

	b.imageSpec, err = img.OCIConfig(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (b *Bundle) extractImage(ctx context.Context, tmpDir string) error {
	var mapOptions umocilayer.MapOptions

	loggerLevel := sylog.GetLevel()
	// set the apex log level, for umoci
	if loggerLevel <= int(sylog.ErrorLevel) {
		// silent option
		apexlog.SetLevel(apexlog.ErrorLevel)
	} else if loggerLevel <= int(sylog.LogLevel) {
		// quiet option
		apexlog.SetLevel(apexlog.WarnLevel)
	} else if loggerLevel < int(sylog.DebugLevel) {
		// verbose option(s) or default
		apexlog.SetLevel(apexlog.InfoLevel)
	} else {
		// debug option
		apexlog.SetLevel(apexlog.DebugLevel)
	}

	// Allow unpacking as non-root
	if os.Geteuid() != 0 {
		mapOptions.Rootless = true

		uidMap, err := idtools.ParseMapping(fmt.Sprintf("0:%d:1", os.Geteuid()))
		if err != nil {
			return fmt.Errorf("error parsing uidmap: %s", err)
		}
		mapOptions.UIDMappings = append(mapOptions.UIDMappings, uidMap)

		gidMap, err := idtools.ParseMapping(fmt.Sprintf("0:%d:1", os.Getegid()))
		if err != nil {
			return fmt.Errorf("error parsing gidmap: %s", err)
		}
		mapOptions.GIDMappings = append(mapOptions.GIDMappings, gidMap)
	}

	engineExt, err := umoci.OpenLayout(tmpDir)
	if err != nil {
		return fmt.Errorf("error opening layout: %s", err)
	}

	// Obtain the manifest
	tmpfsRef, err := ocilayout.ParseReference(tmpDir + ":" + "tmp")
	if err != nil {
		return err
	}
	imageSource, err := tmpfsRef.NewImageSource(ctx, b.sysCtx)
	if err != nil {
		return fmt.Errorf("error creating image source: %s", err)
	}
	manifestData, mediaType, err := imageSource.GetManifest(ctx, nil)
	if err != nil {
		return fmt.Errorf("error obtaining manifest source: %s", err)
	}
	if mediaType != imgspecv1.MediaTypeImageManifest {
		return fmt.Errorf("error verifying manifest media type: %s", mediaType)
	}
	var manifest imgspecv1.Manifest
	json.Unmarshal(manifestData, &manifest)

	// UnpackRootfs from umoci v0.4.2 expects a path to a non-existing directory
	os.RemoveAll(tools.RootFs(b.bundlePath).Path())

	// Unpack root filesystem
	unpackOptions := umocilayer.UnpackOptions{MapOptions: mapOptions}
	err = umocilayer.UnpackRootfs(ctx, engineExt, tools.RootFs(b.bundlePath).Path(), manifest, &unpackOptions)
	if err != nil {
		return fmt.Errorf("error unpacking rootfs: %s", err)
	}

	return nil
}
