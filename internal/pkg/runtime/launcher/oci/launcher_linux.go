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
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/apptainer/apptainer/internal/pkg/buildcfg"
	"github.com/apptainer/apptainer/internal/pkg/cache"
	"github.com/apptainer/apptainer/internal/pkg/cgroups"
	ociclient "github.com/apptainer/apptainer/internal/pkg/client/oci"
	"github.com/apptainer/apptainer/internal/pkg/runtime/launcher"
	"github.com/apptainer/apptainer/internal/pkg/util/fs"
	"github.com/apptainer/apptainer/internal/pkg/util/fs/files"
	"github.com/apptainer/apptainer/internal/pkg/util/rootless"
	imgutil "github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/ocibundle"
	"github.com/apptainer/apptainer/pkg/ocibundle/native"
	"github.com/apptainer/apptainer/pkg/ocibundle/ocisif"
	"github.com/apptainer/apptainer/pkg/ocibundle/tools"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/apptainerconf"
	"github.com/apptainer/apptainer/pkg/util/slice"
	"github.com/container-orchestrated-devices/container-device-interface/pkg/cdi"
	"github.com/google/uuid"
	lccgroups "github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	ErrUnsupportedOption = errors.New("not supported by OCI launcher")
	ErrNotImplemented    = errors.New("not implemented by OCI launcher")

	unsupportedNoMount = []string{"dev", "cwd", "bind-paths"}
)

// Launcher will holds configuration for, and will launch a container using an
// OCI runtime.
type Launcher struct {
	cfg           launcher.Options
	apptainerConf *apptainerconf.File
	// homeSrc is the computed source (on the host) for the user's home directory.
	// An empty value indicates there is no source on the host, and a tmpfs will be used.
	homeSrc string
	// homeDest is the computed destination (in the container) for the user's home directory.
	// An empty value is not valid at mount time.
	homeDest string
}

// NewLauncher returns a oci.Launcher with an initial configuration set by opts.
func NewLauncher(opts ...launcher.Option) (*Launcher, error) {
	lo := launcher.Options{}
	for _, opt := range opts {
		if err := opt(&lo); err != nil {
			return nil, fmt.Errorf("%w", err)
		}
	}

	if err := checkOpts(lo); err != nil {
		return nil, err
	}

	c := apptainerconf.GetCurrentConfig()
	if c == nil {
		return nil, fmt.Errorf("apptainer configuration is not initialized")
	}

	homeSrc, homeDest, err := parseHomeDir(lo.HomeDir, lo.CustomHome, lo.Fakeroot)
	if err != nil {
		return nil, err
	}

	return &Launcher{
		cfg:           lo,
		apptainerConf: c,
		homeSrc:       homeSrc,
		homeDest:      homeDest,
	}, nil
}

// checkOpts ensures that options set are supported by the oci.Launcher.
//
// nolint:maintidx
func checkOpts(lo launcher.Options) error {
	badOpt := []string{}

	if lo.Writable {
		badOpt = append(badOpt, "Writable")
	}
	if lo.WritableTmpfs {
		sylog.Infof("--oci mode uses --writable-tmpfs by default")
	}

	if len(lo.FuseMount) > 0 {
		badOpt = append(badOpt, "FuseMount")
	}

	for _, nm := range lo.NoMount {
		if strings.HasPrefix(nm, "/") || slice.ContainsString(unsupportedNoMount, nm) {
			sylog.Warningf("--no-mount %s is not supported in OCI mode, ignoring.", nm)
		}
	}

	if lo.NvCCLI {
		badOpt = append(badOpt, "NvCCLI")
	}

	if lo.CleanEnv {
		badOpt = append(badOpt, "CleanEnv")
	}
	if lo.NoEval {
		badOpt = append(badOpt, "NoEval")
	}

	// Network always set in CLI layer even if network namespace not requested.
	// We only support isolation at present
	if lo.Namespaces.Net && lo.Network != "none" {
		badOpt = append(badOpt, "Network (except none)")
	}

	if len(lo.NetworkArgs) > 0 {
		badOpt = append(badOpt, "NetworkArgs")
	}

	if lo.AllowSUID {
		badOpt = append(badOpt, "AllowSUID")
	}
	if len(lo.SecurityOpts) > 0 {
		badOpt = append(badOpt, "SecurityOpts")
	}
	if lo.NoUmask {
		badOpt = append(badOpt, "NoUmask")
	}

	// ConfigFile always set by CLI. We should support only the default from build time.
	if lo.ConfigFile != "" && lo.ConfigFile != buildcfg.APPTAINER_CONF_FILE {
		badOpt = append(badOpt, "ConfigFile")
	}

	if lo.ShellPath != "" {
		badOpt = append(badOpt, "ShellPath")
	}

	if lo.Boot {
		badOpt = append(badOpt, "Boot")
	}
	if lo.NoInit {
		badOpt = append(badOpt, "NoInit")
	}
	if lo.Contain {
		badOpt = append(badOpt, "Contain")
	}
	if lo.ContainAll {
		badOpt = append(badOpt, "ContainAll")
	}

	if lo.AppName != "" {
		badOpt = append(badOpt, "AppName")
	}

	if lo.KeyInfo != nil {
		badOpt = append(badOpt, "KeyInfo")
	}

	if lo.SIFFUSE {
		badOpt = append(badOpt, "SIFFUSE")
	}

	if len(badOpt) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupportedOption, strings.Join(badOpt, ","))
	}

	return nil
}

// parseHomeDir parses the homedir value passed from the CLI layer into a host source, and container dest.
// This includes handling fakeroot and custom --home dst, or --home src:dst specifications.
func parseHomeDir(homedir string, custom, fakeroot bool) (src, dest string, err error) {
	// Get the host user's information, looking outside of a user namespace if necessary.
	pw, err := rootless.GetUser()
	if err != nil {
		return "", "", err
	}

	// By default in --oci mode there is no external source for $HOME, i.e. a `tmpfs` will be used.
	src = ""
	// By default the destination in the container matches the users's $HOME on the host.
	dest = pw.HomeDir

	// --fakeroot means we are root in the container so $HOME=/root
	if fakeroot {
		dest = "/root"
	}

	// If the user set a custom --home via the CLI, then override the defaults.
	if custom {
		homeSlice := strings.Split(homedir, ":")
		if len(homeSlice) < 1 || len(homeSlice) > 2 {
			return "", "", fmt.Errorf("home argument has incorrect number of elements: %v", homeSlice)
		}
		// A single path was provided, so we will be mounting a tmpfs on this custom destination.
		dest = homeSlice[0]

		// Two paths provided (<src>:<dest>), so we will be bind mounting from host to container.
		if len(homeSlice) > 1 {
			src = homeSlice[0]
			dest = homeSlice[1]
		}
	}
	return src, dest, nil
}

// createSpec creates an initial OCI runtime specification, suitable to launch a
// container. This spec excludes the Process config, as this has to be computed
// where the image config is available, to account for the image's CMD /
// ENTRYPOINT / ENV / USER. See finalizeSpec() function.
func (l *Launcher) createSpec() (spec *specs.Spec, err error) {
	ms := minimalSpec()
	spec = &ms

	err = addNamespaces(spec, l.cfg.Namespaces)
	if err != nil {
		return nil, err
	}

	if len(l.cfg.Hostname) > 0 {
		// This is a sanity-check; actionPreRun in actions.go should have prevented this scenario from arising.
		if !l.cfg.Namespaces.UTS {
			return nil, fmt.Errorf("internal error: trying to set hostname without UTS namespace")
		}

		spec.Hostname = l.cfg.Hostname
	}

	mounts, err := l.getMounts()
	if err != nil {
		return nil, err
	}
	spec.Mounts = mounts

	cgPath, resources, err := l.getCgroup()
	if err != nil {
		return nil, err
	}
	if cgPath != "" {
		spec.Linux.CgroupsPath = cgPath
		spec.Linux.Resources = resources
	}

	return spec, nil
}

// finalizeSpec updates the bundle config, filling in Process config that depends on the image spec.
func (l *Launcher) finalizeSpec(ctx context.Context, b ocibundle.Bundle, spec *specs.Spec, image string, process string, args []string) (err error) {
	imgSpec := b.ImageSpec()
	if imgSpec == nil {
		return fmt.Errorf("bundle has no image spec")
	}

	// In the absence of a USER in the OCI image config, we will run the
	// container process as our own user / group, i.e. the uid / gid outside of
	// any initial id-mapped user namespace.
	rootlessUID, err := rootless.Getuid()
	if err != nil {
		return fmt.Errorf("while fetching uid: %w", err)
	}
	rootlessGID, err := rootless.Getgid()
	if err != nil {
		return fmt.Errorf("while fetching gid: %w", err)
	}
	currentUID := uint32(rootlessUID)
	currentGID := uint32(rootlessGID)
	targetUID := currentUID
	targetGID := currentGID
	containerUser := false

	// If the OCI image config specifies a USER we will:
	//  * When unprivileged - run as that user, via nested subuid/gid mappings (host user -> userns root -> OCI USER)
	//  * When privileged - directly run as that user, as a host uid/gid.
	if imgSpec.Config.User != "" {
		imgUser, err := tools.BundleUser(b.Path(), imgSpec.Config.User)
		if err != nil {
			return err
		}
		imgUID, err := strconv.ParseUint(imgUser.Uid, 10, 32)
		if err != nil {
			return err
		}
		imgGID, err := strconv.ParseUint(imgUser.Gid, 10, 32)
		if err != nil {
			return err
		}
		targetUID = uint32(imgUID)
		targetGID = uint32(imgGID)
		containerUser = true
		sylog.Debugf("Running as USER specified in OCI image config %d:%d", targetUID, targetGID)
	}

	// Fakeroot always overrides to give us root in the container (via userns & idmap if unprivileged).
	if l.cfg.Fakeroot {
		targetUID = 0
		targetGID = 0
	}

	if targetUID != 0 && currentUID != 0 {
		uidMap, gidMap, err := getReverseUserMaps(currentUID, targetUID, targetGID)
		if err != nil {
			return err
		}
		spec.Linux.UIDMappings = uidMap
		spec.Linux.GIDMappings = gidMap
		// Must add userns to the runc/crun applied config for the inner reverse uid/gid mapping to work.
		spec.Linux.Namespaces = append(
			spec.Linux.Namespaces,
			specs.LinuxNamespace{Type: specs.UserNamespace},
		)
	}

	u := specs.User{
		UID: targetUID,
		GID: targetGID,
	}

	specProcess, err := l.getProcess(ctx, *imgSpec, image, b.Path(), process, args, u)
	if err != nil {
		return err
	}
	spec.Process = specProcess

	if len(l.cfg.CdiDirs) > 0 {
		err = addCDIDevices(spec, l.cfg.Devices, cdi.WithSpecDirs(l.cfg.CdiDirs...))
	} else {
		err = addCDIDevices(spec, l.cfg.Devices)
	}
	if err != nil {
		return err
	}

	// Handle container /etc/[group|passwd|resolv.conf]
	if err := l.prepareEtc(b, spec, containerUser); err != nil {
		return err
	}

	if err := b.Update(ctx, spec); err != nil {
		return err
	}

	return nil
}

// prepareEtc creates modified container-specific /etc files and adds them to
// the spec mount list, to be bound into the assembled container. containerUser
// should be set to true if the runtime user information will be derived from
// the container's existing passwd/group files. spec.Process.User.[UG]ID must be
// set correctly before calling.
func (l *Launcher) prepareEtc(b ocibundle.Bundle, spec *specs.Spec, containerUser bool) error {
	if err := os.Mkdir(filepath.Join(b.Path(), "etc"), 0o755); err != nil && !os.IsExist(err) {
		return err
	}

	resolvMount, err := l.prepareResolvConf(b.Path())
	if err != nil {
		return err
	}
	if resolvMount != nil {
		spec.Mounts = append(spec.Mounts, *resolvMount)
	}

	// If the container specifies a USER, we do not create a customized
	// /etc/passwd|group. All we do is test for a conflicting --home option (in
	// which case, we issue an error) and return
	if containerUser {
		if l.cfg.CustomHome {
			return fmt.Errorf("a custom --home is not currently supported when running containers that declare a USER")
		}
		return nil
	}

	passwdMount, err := l.preparePasswd(b.Path(), spec.Process.User.UID)
	if err != nil {
		return err
	}
	if passwdMount != nil {
		spec.Mounts = append(spec.Mounts, *passwdMount)
	}

	groupMount, err := l.prepareGroup(b.Path(), spec.Process.User.UID, spec.Process.User.GID)
	if err != nil {
		return err
	}
	if groupMount != nil {
		spec.Mounts = append(spec.Mounts, *groupMount)
	}

	return nil
}

// preparePasswd creates an `/etc/passwd` file in the bundle. This is based on
// the passwd file from the pristine rootfs, modified to reflect the container
// user configuration. An appropriate bind mount to use the create file is
// returned on success.
func (l *Launcher) preparePasswd(bundlePath string, uid uint32) (*specs.Mount, error) {
	rootfs := tools.RootFs(bundlePath).Path()
	rootfsPasswd := filepath.Join(rootfs, "etc", "passwd")
	containerPasswd := filepath.Join(bundlePath, "etc", "passwd")

	if !l.apptainerConf.ConfigPasswd {
		sylog.Debugf("Skipping creation of %s due to apptainer.conf", containerPasswd)
		return nil, nil
	}

	sylog.Debugf("Creating container passwd file: %s", containerPasswd)
	content, err := files.Passwd(rootfsPasswd, l.homeDest, int(uid), nil)
	if err != nil {
		// E.g. container doesn't contain an /etc/passwd
		sylog.Warningf("%s", err)
		return nil, nil
	}
	if err := os.WriteFile(containerPasswd, content, 0o755); err != nil {
		return nil, fmt.Errorf("while writing passwd file: %w", err)
	}
	passwdMount := specs.Mount{
		Source:      containerPasswd,
		Destination: "/etc/passwd",
		Type:        "none",
		Options:     []string{"bind"},
	}
	return &passwdMount, nil
}

// prepareGroup creates an `/etc/group` file in the bundle. This is based on
// the group file from the pristine rootfs, modified to reflect the container
// group configuration. An appropriate bind mount to use the create file is
// returned on success.
func (l *Launcher) prepareGroup(bundlePath string, uid, gid uint32) (*specs.Mount, error) {
	rootfs := tools.RootFs(bundlePath).Path()
	rootfsGroup := filepath.Join(rootfs, "etc", "group")
	containerGroup := filepath.Join(bundlePath, "etc", "group")

	if !l.apptainerConf.ConfigGroup {
		sylog.Debugf("Skipping creation of %s due to apptainer.conf", containerGroup)
		return nil, nil
	}

	sylog.Debugf("Creating container group file: %s", containerGroup)
	content, err := files.Group(rootfsGroup, int(uid), []int{int(gid)}, nil)
	if err != nil {
		// E.g. container doesn't contain an /etc/group
		sylog.Warningf("%s", err)
		return nil, nil
	} else if err := os.WriteFile(containerGroup, content, 0o755); err != nil {
		return nil, fmt.Errorf("while writing passwd file: %w", err)
	}
	groupMount := specs.Mount{
		Source:      containerGroup,
		Destination: "/etc/group",
		Type:        "none",
		Options:     []string{"bind"},
	}
	return &groupMount, nil
}

// prepareResolvConfcreates `/etc/resolv.conf` in the bundle. An appropriate
// bind mount to bind over the pristing rootfs `/etc/resolv.conf` is returned on
// success.
func (l *Launcher) prepareResolvConf(bundlePath string) (*specs.Mount, error) {
	hostResolvConfPath := "/etc/resolv.conf"
	containerEtc := filepath.Join(bundlePath, "etc")
	containerResolvConfPath := filepath.Join(containerEtc, "resolv.conf")

	if !l.apptainerConf.ConfigResolvConf {
		sylog.Debugf("Skipping creation of %s due to apptainer.conf", containerResolvConfPath)
		return nil, nil
	}

	var resolvConfData []byte
	var err error
	if len(l.cfg.DNS) > 0 {
		dns := strings.Replace(l.cfg.DNS, " ", "", -1)
		ips := strings.Split(dns, ",")
		for _, ip := range ips {
			if net.ParseIP(ip) == nil {
				return nil, fmt.Errorf("DNS nameserver %v is not a valid IP address", ip)
			}
			line := fmt.Sprintf("nameserver %s\n", ip)
			resolvConfData = append(resolvConfData, line...)
		}
	} else {
		resolvConfData, err = os.ReadFile(hostResolvConfPath)
		if err != nil {
			return nil, fmt.Errorf("could not read host's resolv.conf file: %w", err)
		}
	}

	if err := os.WriteFile(containerResolvConfPath, resolvConfData, 0o755); err != nil {
		return nil, fmt.Errorf("while writing container's resolv.conf file: %v", err)
	}

	resolvMount := specs.Mount{
		Source:      containerResolvConfPath,
		Destination: "/etc/resolv.conf",
		Type:        "none",
		Options:     []string{"bind"},
	}

	return &resolvMount, nil
}

// Exec will interactively execute a container via the runc low-level runtime.
// image is a reference to an OCI image, e.g. docker://ubuntu or oci:/tmp/mycontainer
func (l *Launcher) Exec(ctx context.Context, image string, process string, args []string, instanceName string) error {
	if instanceName != "" {
		return fmt.Errorf("%w: instanceName", ErrNotImplemented)
	}

	if l.cfg.SysContext == nil {
		return fmt.Errorf("launcher SysContext must be set for OCI image handling")
	}

	// Handle bare image paths and check image file format.
	image, err := normalizeImageRef(image)
	if err != nil {
		return err
	}

	if err := l.mountSessionTmpfs(); err != nil {
		return err
	}

	bundleDir, err := os.MkdirTemp(buildcfg.SESSIONDIR, "oci-bundle")
	if err != nil {
		return nil
	}
	defer func() {
		sylog.Debugf("Removing OCI bundle at: %s", bundleDir)
		if cleanupErr := fs.ForceRemoveAll(bundleDir); cleanupErr != nil {
			sylog.Errorf("Couldn't remove OCI bundle %s: %v", bundleDir, cleanupErr)
		}
	}()

	sylog.Debugf("Creating OCI bundle at: %s", bundleDir)

	var imgCache *cache.Handle
	if !l.cfg.CacheDisabled {
		imgCache, err = cache.New(cache.Config{
			ParentDir: os.Getenv(cache.DirEnv),
		})
		if err != nil {
			return err
		}
	}

	// Create OCI runtime spec, excluding the Process settings which must consider the image spec.
	spec, err := l.createSpec()
	if err != nil {
		return fmt.Errorf("while creating OCI spec: %w", err)
	}

	// Create a bundle - obtain and extract the image.
	var b ocibundle.Bundle
	if strings.HasPrefix(image, "oci-sif:") {
		b, err = ocisif.New(
			ocisif.OptBundlePath(bundleDir),
			ocisif.OptImageRef(image),
		)
	} else {
		b, err = native.New(
			native.OptBundlePath(bundleDir),
			native.OptImageRef(image),
			native.OptSysCtx(l.cfg.SysContext),
			native.OptImgCache(imgCache),
		)
	}
	if err != nil {
		return err
	}
	if err := b.Create(ctx, spec); err != nil {
		return err
	}

	// With reference to the bundle's image spec, now set the process configuration.
	if err := l.finalizeSpec(ctx, b, spec, image, process, args); err != nil {
		return err
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("while generating container id: %w", err)
	}

	// Execution of runc/crun run, wrapped with overlay prep / cleanup.
	err = RunWrapped(ctx, id.String(), b.Path(), "", l.cfg.OverlayPaths, l.apptainerConf.SystemdCgroups)

	// Unmounts pristine rootfs from bundle, and removes the bundle.
	if cleanupErr := b.Delete(ctx); cleanupErr != nil {
		sylog.Errorf("Couldn't cleanup bundle: %v", err)
	}

	if err := l.unmountSessionTmpfs(); err != nil {
		sylog.Errorf("Couldn't unmount session directory: %v", err)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	return err
}

// getCgroup will return a cgroup path and resources for the runtime to create.
func (l *Launcher) getCgroup() (path string, resources *specs.LinuxResources, err error) {
	if l.cfg.CGroupsJSON == "" {
		return "", nil, nil
	}
	path = cgroups.DefaultPathForPid(l.apptainerConf.SystemdCgroups, -1)
	resources, err = cgroups.UnmarshalJSONResources(l.cfg.CGroupsJSON)
	if err != nil {
		return "", nil, err
	}
	return path, resources, nil
}

// mountSessionTmpfs mounts a tmpfs onto buildcfg.SESSIONDIR
func (l *Launcher) mountSessionTmpfs() error {
	sylog.Debugf("Mounting %d MiB tmpfs to %s", l.apptainerConf.SessiondirMaxSize, buildcfg.SESSIONDIR)
	options := fmt.Sprintf("mode=1777,size=%dm", l.apptainerConf.SessiondirMaxSize)
	if err := syscall.Mount("tmpfs", buildcfg.SESSIONDIR, "tmpfs", syscall.MS_NODEV, options); err != nil {
		return fmt.Errorf("failed to mount session tmpfs %s: %w", buildcfg.SESSIONDIR, err)
	}
	return nil
}

// unmountSessionTmpfs mounts a tmpfs onto buildcfg.SESSIONDIR
func (l *Launcher) unmountSessionTmpfs() error {
	sylog.Debugf("Umounting tmpfs from %s", buildcfg.SESSIONDIR)
	if err := syscall.Unmount(buildcfg.SESSIONDIR, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("failed to unmount session tmpfs %s: %w", buildcfg.SESSIONDIR, err)
	}
	return nil
}

// crunNestCgroup will check whether we are using crun, and enter a cgroup if
// running as a non-root user under cgroups v2, with systemd. This is required
// to satisfy a common user-owned ancestor cgroup requirement on e.g. bare ssh
// logins. See: https://github.com/sylabs/singularity/issues/1538
func CrunNestCgroup() error {
	c := apptainerconf.GetCurrentConfig()
	if c == nil {
		return fmt.Errorf("apptainer configuration is not initialized")
	}

	r, err := runtime()
	if err != nil {
		return err
	}

	// No workaround required for runc.
	if filepath.Base(r) == "runc" {
		return nil
	}

	// No workaround required if we are run as root.
	if os.Getuid() == 0 {
		return nil
	}

	// We can only create a new cgroup under cgroups v2 with systemd as manager.
	// Generally we won't hit the issue that needs a workaround under cgroups v1, so no-op instead of a warning here.
	if !(lccgroups.IsCgroup2UnifiedMode() && c.SystemdCgroups) {
		return nil
	}

	// We are running crun as a user. Enter a cgroup now.
	pid := os.Getpid()
	sylog.Debugf("crun workaround - adding process %d to sibling cgroup", pid)
	manager, err := cgroups.NewManagerWithSpec(&specs.LinuxResources{}, pid, "", c.SystemdCgroups)
	if err != nil {
		return fmt.Errorf("couldn't create cgroup manager: %w", err)
	}
	cgPath, _ := manager.GetCgroupRelPath()
	sylog.Debugf("In sibling cgroup: %s", cgPath)

	return nil
}

func mergeMap(a map[string]string, b map[string]string) map[string]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// normalizeImageRef transforms a bare image path to an oci-sif: prefixed path,
// after checking the image is an oci-sif.
func normalizeImageRef(imageRef string) (string, error) {
	imageRef = strings.TrimPrefix(imageRef, "oci-sif:")

	// We can't just look for a `<transport>:<path>` pair as bare filenames can contain colons.
	// If we don't match a supported oci source transport, assume we have a bare filename.
	parts := strings.SplitN(imageRef, ":", 2)
	if len(parts) == 2 && ociclient.IsSupported(parts[0]) != "" {
		return imageRef, nil
	}

	// oci-sif or bare image path, check it's an image we can run.
	img, err := imgutil.Init(imageRef, false)
	if err != nil {
		return "", err
	}
	defer img.File.Close()

	switch img.Type {
	case imgutil.OCISIF:
		return "oci-sif:" + imageRef, nil
	case imgutil.SQUASHFS, imgutil.ENCRYPTSQUASHFS:
		return "", fmt.Errorf("OCI mode cannot run bare squashfs images")
	case imgutil.EXT3:
		return "", fmt.Errorf("OCI mode cannot run bare ext3 images")
	case imgutil.SANDBOX:
		return "", fmt.Errorf("OCI mode cannot run bare directory/sandbox images")
	case imgutil.SIF:
		return "", fmt.Errorf("OCI mode cannot run non-OCI SIF images")
	case imgutil.RAW:
		return "", fmt.Errorf("OCI mode cannot run raw images")
	}

	return "", fmt.Errorf("OCI mode cannot run unknown image type %d", img.Type)
}
