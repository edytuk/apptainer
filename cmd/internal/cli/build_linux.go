// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2020, Control Command Inc. All rights reserved.
// Copyright (c) 2018-2021, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/apptainer/apptainer/internal/pkg/build"
	"github.com/apptainer/apptainer/internal/pkg/buildcfg"
	"github.com/apptainer/apptainer/internal/pkg/cache"
	"github.com/apptainer/apptainer/internal/pkg/fakefake"
	"github.com/apptainer/apptainer/internal/pkg/fakeroot"
	"github.com/apptainer/apptainer/internal/pkg/remote/endpoint"
	fakerootConfig "github.com/apptainer/apptainer/internal/pkg/runtime/engine/fakeroot/config"
	"github.com/apptainer/apptainer/internal/pkg/util/env"
	"github.com/apptainer/apptainer/internal/pkg/util/fs"
	"github.com/apptainer/apptainer/internal/pkg/util/interactive"
	"github.com/apptainer/apptainer/internal/pkg/util/rootless"
	"github.com/apptainer/apptainer/internal/pkg/util/starter"
	"github.com/apptainer/apptainer/internal/pkg/util/user"
	"github.com/apptainer/apptainer/pkg/build/types"
	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/runtime/engine/config"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/cryptkey"
	"github.com/apptainer/apptainer/pkg/util/namespaces"
	keyClient "github.com/apptainer/container-key-client/client"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/spf13/cobra"
)

func fakerootExec(isDeffile, unprivEncrypt bool) {
	useSuid := buildcfg.APPTAINER_SUID_INSTALL == 1 && !buildArgs.userns

	// First remove fakeroot option from args and environment if present
	short := "-" + buildFakerootFlag.ShortHand
	long := "--" + buildFakerootFlag.Name
	for _, pfx := range env.ApptainerPrefixes {
		envKey := fmt.Sprintf("%s_%s", pfx, buildFakerootFlag.EnvKeys[0])
		if os.Getenv(envKey) != "" {
			os.Unsetenv(envKey)
		}
	}
	var args []string
	for i, arg := range os.Args {
		if i == 0 {
			path, _ := osExec.LookPath(arg)
			arg = path
		}
		// This does not treat options before the "build" command
		//   differently than after, which is OK currently because
		//   there is no -f defined there
		if arg != short && arg != long {
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				// remove all f within the multiple short options
				arg = strings.ReplaceAll(arg, buildFakerootFlag.ShortHand, "")
				if arg == "-" {
					// could have been -ff
					continue
				}
			}
			args = append(args, arg)
		}
	}

	var err error
	uid := uint32(os.Getuid())

	// Append the user's real UID/GID to the environment as _CONTAINERS_ROOTLESS_UID/GID.
	// This is required in fakeroot builds that may use containers/image 5.7 and above.
	// https://github.com/containers/image/issues/1066
	// https://github.com/containers/image/blob/master/internal/rootless/rootless.go
	os.Setenv(rootless.UIDEnv, strconv.Itoa(os.Getuid()))
	os.Setenv(rootless.GIDEnv, strconv.Itoa(os.Getgid()))

	if uid != 0 && (!fakeroot.IsUIDMapped(uid) || buildArgs.ignoreSubuid) {
		sylog.Infof("User not listed in %v, trying root-mapped namespace", fakeroot.SubUIDFile)
		os.Setenv("_APPTAINER_FAKEFAKEROOT", "1")
		if buildArgs.ignoreUserns {
			err = errors.New("could not start root-mapped namespace because --ignore-userns is set")
		} else {
			err = fakefake.UnshareRootMapped(args)
		}
		if err == nil {
			// All the work has been done by the child process
			os.Exit(0)
		}
		sylog.Debugf("UnshareRootMapped failed: %v", err)
		sylog.Infof("Could not start root-mapped namespace")
		if !useSuid && isDeffile {
			sylog.Fatalf("Building from a definition file unprivileged requires either a suid installation or unprivileged user namespaces")
		}
		if unprivEncrypt {
			sylog.Fatalf("Building with encryption unprivileged requires unprivileged user namespaces")
		}
		// Returning from here at this point will go on to try
		// the fakeroot command below
		return
	}

	if buildArgs.nvccli && !buildArgs.noTest {
		sylog.Warningf("Due to writable-tmpfs limitations, %%test sections will fail with --nvccli & --fakeroot")
		sylog.Infof("Use -T / --notest to disable running tests during the build")
	}

	user, err := user.GetPwUID(uid)
	if err != nil {
		sylog.Fatalf("failed to retrieve user information: %s", err)
	}

	engineConfig := &fakerootConfig.EngineConfig{
		Args:     args,
		Envs:     os.Environ(),
		Home:     user.Dir,
		BuildEnv: true,
	}

	cfg := &config.Common{
		EngineName:   fakerootConfig.Name,
		ContainerID:  "fakeroot",
		EngineConfig: engineConfig,
	}

	err = starter.Exec(
		"Apptainer fakeroot",
		cfg,
		starter.UseSuid(useSuid),
	)
	sylog.Fatalf("%s", err)
}

func runBuild(cmd *cobra.Command, args []string) {
	dest := args[0]
	spec := args[1]

	fakerootPath := ""
	if os.Getenv("_APPTAINER_FAKEFAKEROOT") == "1" {
		var err error
		uid := os.Getuid()
		if uid == 0 {
			// Try to bind-mount the original user's home directory to /root.
			// This enables things like git clone to work in the %setup section
			// of a definition file.
			homedir := os.Getenv("HOME")
			if homedir != "" {
				err = syscall.Mount(homedir, "/root", "", syscall.MS_BIND, "")
				if err != nil {
					sylog.Debugf("Failure bind-mounting %s to /root: %v, skipping", homedir, err)
				} else {
					sylog.Debugf("Bind-mounting %s to /root", homedir)
				}
			}
		}
		// Try fakeroot command
		os.Unsetenv("_APPTAINER_FAKEFAKEROOT")
		buildArgs.fakeroot = false
		if buildArgs.ignoreFakerootCmd {
			err = errors.New("fakeroot command is ignored because of --ignore-fakeroot-command")
		} else {
			fakerootPath, err = fakefake.FindFake()
		}
		if err != nil {
			sylog.Infof("fakeroot command not found")
			if uid != 0 {
				if fs.IsFile(spec) && !isImage(spec) {
					sylog.Fatalf("Building from a definition file requires root or some kind of fake root")
				}
				// else it must have been explicitly requested
				sylog.Fatalf("Cannot start any kind of fake root")
			}
			sylog.Infof("Installing some packages may fail")
		} else {
			sylog.Infof("The %%post section will be run under fakeroot")
			if !buildArgs.fixPerms && uid != 0 {
				sylog.Infof("Using --fix-perms because building from a definition file")
				sylog.Infof(" without either root user or unprivileged user namespaces")
				buildArgs.fixPerms = true
			}
		}
	}

	if buildArgs.nvidia {
		os.Setenv("APPTAINER_NV", "1")
	}
	if buildArgs.nvccli {
		os.Setenv("APPTAINER_NVCCLI", "1")
	}
	if buildArgs.rocm {
		os.Setenv("APPTAINER_ROCM", "1")
	}
	if len(buildArgs.bindPaths) > 0 {
		os.Setenv("APPTAINER_BINDPATH", strings.Join(buildArgs.bindPaths, ","))
	}
	if len(buildArgs.mounts) > 0 {
		os.Setenv("APPTAINER_MOUNT", strings.Join(buildArgs.mounts, "\n"))
	}
	if buildArgs.writableTmpfs {
		if buildArgs.fakeroot {
			sylog.Fatalf("--writable-tmpfs option is not supported for fakeroot build")
		}
		os.Setenv("APPTAINER_WRITABLE_TMPFS", "1")
	}

	// check if target collides with existing file
	if err := checkBuildTarget(dest); err != nil {
		sylog.Fatalf("While checking build target: %s", err)
	}

	runBuildLocal(cmd.Context(), cmd, dest, spec, fakerootPath)
	sylog.Infof("Build complete: %s", dest)
}

func runBuildLocal(ctx context.Context, cmd *cobra.Command, dst, spec string, fakerootPath string) {
	var keyInfo *cryptkey.KeyInfo
	unprivilege := false
	if buildArgs.encrypt || promptForPassphrase || cmd.Flags().Lookup("pem-path").Changed {
		if namespaces.IsUnprivileged() {
			unprivilege = true
		}

		k, err := getEncryptionMaterial(cmd)
		if err != nil {
			sylog.Fatalf("While handling encryption material: %v", err)
		}
		keyInfo = k

		if keyInfo == nil && unprivilege {
			sylog.Errorf("Missing encryption info, please add `--passphrase` or `--pem-path` or corresponding environment variable")
			return
		}
	} else {
		_, passphraseEnvOK := os.LookupEnv("APPTAINER_ENCRYPTION_PASSPHRASE")
		_, pemPathEnvOK := os.LookupEnv("APPTAINER_ENCRYPTION_PEM_PATH")
		if passphraseEnvOK || pemPathEnvOK {
			sylog.Warningf("Encryption related env vars found, but --encrypt was not specified. NOT encrypting container.")
		}
	}

	imgCache := getCacheHandle(cache.Config{Disable: disableCache})
	if imgCache == nil {
		sylog.Fatalf("Failed to create an image cache handle")
	}

	err := checkSections()
	if err != nil {
		sylog.Fatalf("Could not check build sections: %v", err)
	}

	authConf, err := makeDockerCredentials(cmd)
	if err != nil {
		sylog.Fatalf("While creating Docker credentials: %v", err)
	}

	// parse definition to determine build source
	defs, err := build.MakeAllDefs(spec)
	if err != nil {
		sylog.Fatalf("Unable to build from %s: %v", spec, err)
	}

	authToken := ""
	hasLibrary := false
	libraryURL := ""
	hasSIF := false

	for _, d := range defs {
		// If there's a library source we need the library client, and it'll be a SIF
		if d.Header["bootstrap"] == "library" {
			hasLibrary = true
			hasSIF = true
		}
		if val, ok := d.Header["library"]; ok {
			libraryURL = val
		}
		// Certain other bootstrap sources may result in a SIF image source
		if d.Header["bootstrap"] == "localimage" || d.Header["bootstrap"] == "oras" || d.Header["bootstrap"] == "shub" {
			hasSIF = true
		}
	}

	defs, err = processDefs(buildArgs.buildVarArgs, buildArgs.buildVarArgFile, defs)
	if err != nil {
		sylog.Fatalf("While processing the definition file: %v", err)
	}

	// We only need to initialize the library client if we have a library source
	// in our definition file.
	if hasLibrary {
		if buildArgs.libraryURL == "" && libraryURL != "" {
			buildArgs.libraryURL = libraryURL
		}
		lc, err := getLibraryClientConfig(buildArgs.libraryURL)
		if err != nil {
			sylog.Fatalf("Unable to get library client configuration: %v", err)
		}
		buildArgs.libraryURL = lc.BaseURL
		authToken = lc.AuthToken
	}

	// We only need to initialize the key server client if we have a source
	// in our definition file that could provide a SIF. Only SIFs verify in the build.
	var ko []keyClient.Option
	if hasSIF {
		ko, err = getKeyserverClientOpts(buildArgs.keyServerURL, endpoint.KeyserverVerifyOp)
		if err != nil {
			// Do not hard fail if we can't get a keyserver config.
			// Verification can use the local keyring still.
			sylog.Warningf("Unable to get key server client configuration: %v", err)
		}
	}

	buildFormat := "sif"
	sandboxTarget := false
	if buildArgs.sandbox {
		buildFormat = "sandbox"
		sandboxTarget = true

	}

	b, err := build.New(
		defs,
		build.Config{
			Dest:      dst,
			Format:    buildFormat,
			NoCleanUp: buildArgs.noCleanUp,
			Opts: types.Options{
				ImgCache:          imgCache,
				TmpDir:            tmpDir,
				NoCache:           disableCache,
				Update:            buildArgs.update,
				Force:             forceOverwrite,
				Sections:          buildArgs.sections,
				NoTest:            buildArgs.noTest,
				NoHTTPS:           noHTTPS,
				LibraryURL:        buildArgs.libraryURL,
				LibraryAuthToken:  authToken,
				FakerootPath:      fakerootPath,
				KeyServerOpts:     ko,
				DockerAuthConfig:  authConf,
				DockerDaemonHost:  dockerHost,
				EncryptionKeyInfo: keyInfo,
				FixPerms:          buildArgs.fixPerms,
				SandboxTarget:     sandboxTarget,
				Unprivilege:       unprivilege,
			},
		})
	if err != nil {
		sylog.Fatalf("Unable to create build: %v", err)
	}

	if err = b.Full(ctx); err != nil {
		sylog.Fatalf("While performing build: %v", err)
	}
}

func checkSections() error {
	var all, none bool
	for _, section := range buildArgs.sections {
		if section == "none" {
			none = true
		}
		if section == "all" {
			all = true
		}
	}

	if all && len(buildArgs.sections) > 1 {
		return fmt.Errorf("section specification error: cannot have all and any other option")
	}
	if none && len(buildArgs.sections) > 1 {
		return fmt.Errorf("section specification error: cannot have none and any other option")
	}

	return nil
}

func isImage(spec string) bool {
	i, err := image.Init(spec, false)
	if i != nil {
		_ = i.File.Close()
	}
	return err == nil
}

// getEncryptionMaterial handles the setting of encryption environment and flag parameters to eventually be
// passed to the crypt package for handling.
// This handles the APPTAINER_ENCRYPTION_PASSPHRASE/PEM_PATH envvars outside of cobra in order to
// enforce the unique flag/env precedence for the encryption flow
func getEncryptionMaterial(cmd *cobra.Command) (*cryptkey.KeyInfo, error) {
	passphraseFlag := cmd.Flags().Lookup("passphrase")
	PEMFlag := cmd.Flags().Lookup("pem-path")
	passphraseEnv, passphraseEnvOK := os.LookupEnv("APPTAINER_ENCRYPTION_PASSPHRASE")
	pemPathEnv, pemPathEnvOK := os.LookupEnv("APPTAINER_ENCRYPTION_PEM_PATH")

	// checks for no flags/envvars being set
	if !(PEMFlag.Changed || pemPathEnvOK || passphraseFlag.Changed || passphraseEnvOK) {
		return nil, nil
	}

	// order of precedence:
	// 1. PEM flag
	// 2. Passphrase flag
	// 3. PEM envvar
	// 4. Passphrase envvar

	if PEMFlag.Changed {
		exists, err := fs.PathExists(encryptionPEMPath)
		if err != nil {
			sylog.Fatalf("Unable to verify existence of %s: %v", encryptionPEMPath, err)
		}

		if !exists {
			sylog.Fatalf("Specified PEM file %s: does not exist.", encryptionPEMPath)
		}

		sylog.Verbosef("Using pem path flag for encrypted container")

		// Check it's a valid PEM public key we can load, before starting the build (#4173)
		if cmd.Name() == "build" {
			if _, err := cryptkey.LoadPEMPublicKey(encryptionPEMPath); err != nil {
				sylog.Fatalf("Invalid encryption public key: %v", err)
			}
			// or a valid private key before launching the engine for actions on a container (#5221)
		} else {
			if _, err := cryptkey.LoadPEMPrivateKey(encryptionPEMPath); err != nil {
				sylog.Fatalf("Invalid encryption private key: %v", err)
			}
		}

		return &cryptkey.KeyInfo{Format: cryptkey.PEM, Path: encryptionPEMPath}, nil
	}

	if passphraseFlag.Changed {
		sylog.Verbosef("Using interactive passphrase entry for encrypted container")
		passphrase, err := interactive.AskQuestionNoEcho("Enter encryption passphrase: ")
		if err != nil {
			return nil, err
		}
		if passphrase == "" {
			sylog.Fatalf("Cannot encrypt container with empty passphrase")
		}
		return &cryptkey.KeyInfo{Format: cryptkey.Passphrase, Material: passphrase}, nil
	}

	if pemPathEnvOK {
		exists, err := fs.PathExists(pemPathEnv)
		if err != nil {
			sylog.Fatalf("Unable to verify existence of %s: %v", pemPathEnv, err)
		}

		if !exists {
			sylog.Fatalf("Specified PEM file %s: does not exist.", pemPathEnv)
		}

		sylog.Verbosef("Using pem path environment variable for encrypted container")
		return &cryptkey.KeyInfo{Format: cryptkey.PEM, Path: pemPathEnv}, nil
	}

	if passphraseEnvOK {
		sylog.Verbosef("Using passphrase environment variable for encrypted container")
		return &cryptkey.KeyInfo{Format: cryptkey.Passphrase, Material: passphraseEnv}, nil
	}

	return nil, nil
}

func processDefs(args []string, argFile string, defs []types.Definition) ([]types.Definition, error) {
	// --build-arg and content in --build-arg-file are applied to all stages
	buildArgMap, err := readBuildArgs(args, argFile)
	if err != nil {
		return defs, err
	}

	provideKeys := hashset.New()
	matchedKeys := hashset.New()
	for k := range buildArgMap {
		provideKeys.Add(k)
	}
	// start replacing the variable defined in the definition file
	for idx, def := range defs {
		d, dmap, matched, err := updateDef(&def, buildArgMap)
		if err != nil {
			return defs, fmt.Errorf("while updating the definition file with build args, err: %w", err)
		}

		for k := range dmap {
			provideKeys.Add(k)
		}
		for _, k := range matched {
			matchedKeys.Add(k)
		}
		defs[idx] = *d
	}

	diff := provideKeys.Difference(matchedKeys)
	if !diff.Empty() {
		vars := strings.Fields(strings.TrimPrefix(diff.String(), "HashSet"))

		// All mismatched keys are in provided keys
		if buildArgs.buildArgsUnusedWarn {
			sylog.Warningf("Unused build args: %s", strings.Join(vars, " "))
		} else {
			return defs, fmt.Errorf("unused build args: %s. Use option --warn-unused-build-args to show a warning instead of a fatal message", strings.Join(vars, " "))
		}
	}

	types.UpdateDefinitionRaw(defs)

	return defs, nil
}

func readBuildArgs(args []string, argFile string) (map[string]string, error) {
	buildVarsMap := make(map[string]string)
	if argFile != "" {
		file, err := os.Open(argFile)
		if err != nil {
			return buildVarsMap, fmt.Errorf("while opening the file %s, err: %w", argFile, err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			text := scanner.Text()
			k, v, err := getKeyVal(text)
			if err != nil {
				sylog.Warningf("Skipping the line, err: %v", err)
				continue
			}

			buildVarsMap[k] = v
		}

		if err := scanner.Err(); err != nil {
			return buildVarsMap, fmt.Errorf("while scanning the content of target file %s, err: %w", argFile, err)
		}
	}

	for _, arg := range args {
		k, v, err := getKeyVal(arg)
		if err != nil {
			sylog.Warningf("Skipping the line, err: %v", err)
			continue
		}

		buildVarsMap[k] = v
	}

	return buildVarsMap, nil
}

func getKeyVal(text string) (string, string, error) {
	if !strings.Contains(text, "=") {
		return "", "", fmt.Errorf("text: %s is not `key=value` pair format", text)
	}

	matches := strings.SplitN(text, "=", -1)
	if len(matches) != 2 {
		return "", "", fmt.Errorf("text: %s is not `key=value` pair format", text)
	}

	key := strings.TrimSpace(matches[0])
	if key == "" {
		return "", "", fmt.Errorf("key field is missing in text: %s", text)
	}
	val := strings.TrimSpace(matches[1])
	if val == "" {
		return "", "", fmt.Errorf("value field is missing in text: %s", text)
	}
	return key, val, nil
}

var errNoChange = errors.New("no change to text")

func replaceVar(text []byte, buildArgsMap map[string]string, deffArgsMap map[string]string) ([]byte, []string, error) {
	r := regexp.MustCompile(`{{\s*(\w+)\s*}}`)
	matches := r.FindAllSubmatch(text, -1)
	if matches == nil {
		return text, nil, errNoChange
	}

	var matchedKeys []string
	for _, match := range matches {
		if val, ok := buildArgsMap[string(match[1])]; ok {
			text = bytes.ReplaceAll(text, match[0], []byte(val))
			matchedKeys = append(matchedKeys, string(match[1]))
		} else if val, ok := deffArgsMap[string(match[1])]; ok {
			text = bytes.ReplaceAll(text, match[0], []byte(val))
			matchedKeys = append(matchedKeys, string(match[1]))
		} else {
			return text, matchedKeys, fmt.Errorf("build var %s is not defined through either --build-arg (--build-arg-file) or 'arguments' section", match[1])
		}
	}

	return text, matchedKeys, nil
}

func updateDef(def *types.Definition, buildArgsMap map[string]string) (*types.Definition, map[string]string, []string, error) {
	deffArgsMap := make(map[string]string)
	if def.BuildData.Arguments.Script != "" {
		scanner := bufio.NewScanner(strings.NewReader(def.BuildData.Arguments.Script))
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text != "" && !strings.HasPrefix(text, "#") {
				k, v, err := getKeyVal(text)
				if err != nil {
					sylog.Warningf("Skipping the line, err: %v", err)
					continue
				}
				deffArgsMap[k] = v
			}
		}

		if scanner.Err() != nil {
			return nil, deffArgsMap, nil, fmt.Errorf("while scanning string from 'arguments' section, err: %w", scanner.Err())
		}
	}

	data, err := json.Marshal(def)
	if err != nil {
		return nil, deffArgsMap, nil, fmt.Errorf("while marshaling the Definition struct, err: %w", err)
	}

	content, matchedKeys, err := replaceVar(data, buildArgsMap, deffArgsMap)
	if err != nil && err != errNoChange {
		return nil, deffArgsMap, nil, fmt.Errorf("while replacing var marshaled definition struct, err: %w", err)
	}

	var d types.Definition
	err = json.Unmarshal([]byte(content), &d)
	if err != nil {
		return nil, deffArgsMap, nil, fmt.Errorf("while unmarshal into definition struct, err: %w", err)
	}

	return &d, deffArgsMap, matchedKeys, nil
}
