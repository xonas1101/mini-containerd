package main

// tool -> docker -> container runtime & manager
// This is a minimal container runtime-like program that:
// - creates new namespaces
// - sets hostname
// - chroots into a rootfs
// - mounts proc/sys/cgroup
// - then runs a command inside that isolated environment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli/v3"
	cp "github.com/otiai10/copy"
)

func loadSpec(bundle string) (*specs.Spec, error) {
	data, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read bundle config: %w", err)
	}

	var spec specs.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse bundle config: %w", err)
	}
	return &spec, nil
}

var debugMode bool

func main() {
	// child is an internal subcommand spawned by run() via /proc/self/exe — not user-facing
	if len(os.Args) > 1 && os.Args[1] == "child" {
		if err := child(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	app := &cli.Command{
		Name:  "mini-containerd",
		Usage: "a minimal container runtime",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "debug",
				Usage:       "show full error detail instead of a friendly message",
				Destination: &debugMode,
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "run",
				Usage:     "run a container from a bundle",
				ArgsUsage: "<bundle>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "image",
						Usage: "name of a loaded image to use as rootfs (see `load`/`images`)",
					},
					&cli.StringFlag{
						Name:  "name",
						Usage: "container name (defaults to a random id)",
					},
					&cli.StringFlag{
						Name:  "memory",
						Usage: "memory limit, e.g. 256m, 1g (cgroup v2 memory.max)",
					},
					&cli.FloatFlag{
						Name:  "cpus",
						Usage: "cpu limit in cores, e.g. 0.5 (cgroup v2 cpu.max)",
					},
					&cli.StringSliceFlag{
						Name:  "volume",
						Usage: "bind mount host:container[:ro], repeatable",
					},
					&cli.BoolFlag{
						Name:  "detach",
						Usage: "run the container in the background",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					bundle := cmd.Args().First()
					if bundle == "" {
						return fmt.Errorf("bundle path required")
					}
					if cmd.IsSet("name") && cmd.String("name") == "" {
						return fmt.Errorf("container name cannot be empty")
					}
					if needDelegate() {
						return reexecInDelegate(20)
					}
					return run(RunOptions{
						Bundle:  bundle,
						Image:   cmd.String("image"),
						Name:    cmd.String("name"),
						Memory:  cmd.String("memory"),
						Detach:  cmd.Bool("detach"),
						CPUs:    cmd.Float("cpus"),
						Volumes: cmd.StringSlice("volume"),
					})
				},
			},
			{
				Name:      "load",
				Aliases:   []string{"import"},
				Usage:     "load an image from a tar/tar.gz rootfs bundle",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "file",
						Usage:    "path to the .tar or .tar.gz bundle",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return fmt.Errorf("image name required")
					}
					return LoadImage(name, cmd.String("file"))
				},
			},
			{
				Name:  "images",
				Usage: "list loaded images",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					images, err := ListImages()
					if err != nil {
						return err
					}
					fmt.Printf("%-16s %-20s %-10s %s\n", "NAME", "IMAGE ID", "SIZE", "CREATED")
					for _, img := range images {
						id := img.ID
						if len(id) > 19 {
							id = id[:19]
						}
						fmt.Printf("%-16s %-20s %-10s %s\n",
							img.Name, id, formatSize(img.SizeBytes), img.Created.Format(time.RFC3339))
					}
					return nil
				},
			},
			{
				Name:      "logs",
				Usage:     "show a container's captured stdout/stderr",
				ArgsUsage: "<container>",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "tail",
						Usage: "only show the last N lines",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := cmd.Args().First()
					if id == "" {
						return fmt.Errorf("container name required")
					}
					return ShowLogs(id, int(cmd.Int("tail")))
				},
			},
			{
				Name:      "start",
				Usage:     "restart a stopped container",
				ArgsUsage: "<container>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := cmd.Args().First()
					if id == "" {
						return fmt.Errorf("container name required")
					}
					return StartContainer(id)
				},
			},
			{
				Name:      "stop",
				Usage:     "gracefully stop a running container (SIGTERM, then SIGKILL after a timeout)",
				ArgsUsage: "<container>",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "timeout",
						Usage: "seconds to wait before SIGKILL",
						Value: 10,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := cmd.Args().First()
					if id == "" {
						return fmt.Errorf("container name required")
					}
					return StopContainer(id, time.Duration(cmd.Int("timeout"))*time.Second)
				},
			},
			{
				Name:      "kill",
				Usage:     "immediately force-kill a running container (SIGKILL)",
				ArgsUsage: "<container>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := cmd.Args().First()
					if id == "" {
						return fmt.Errorf("container name required")
					}
					return KillContainer(id)
				},
			},
			{
				Name:      "exec",
				Usage:     "run a command inside a running container's namespaces",
				ArgsUsage: "<container> <command> [args...]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()
					if len(args) < 2 {
						return fmt.Errorf("usage: exec <container> <command> [args...]")
					}
					return ExecInContainer(args[0], args[1:])
				},
			},
		},
	}

	defer func() {
		if r := recover(); r != nil {
			if debugMode {
				panic(r)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", r)
			os.Exit(1)
		}
	}()

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type RunOptions struct {
	Bundle  string
	Image   string
	Name    string
	Memory  string
	CPUs    float64
	Volumes []string
	Detach  bool
}

func run(opts RunOptions) error {
	bundle := opts.Bundle
	spec, err := loadSpec(bundle)
	if err != nil {
		return err
	}

	imageRef := bundle
	rootfsSrc := filepath.Join(bundle, spec.Root.Path)
	if opts.Image != "" {
		if filepath.Base(opts.Image) != opts.Image {
			return fmt.Errorf("invalid image name %q", opts.Image)
		}
		if _, err := os.Stat(imageRootfs(opts.Image)); err != nil {
			return fmt.Errorf("image %q not found: %w", opts.Image, err)
		}
		imageRef = opts.Image
		rootfsSrc = imageRootfs(opts.Image)
	}

	for _, v := range opts.Volumes {
		if _, _, _, err := parseVolume(v); err != nil {
			return err
		}
	}

	c := NewContainer(imageRef, spec.Process.Args)
	c.Bundle = bundle
	c.Volumes = opts.Volumes
	if opts.Name != "" {
		c.ID = opts.Name
	}
	if _, err := os.Stat(containerDir(c.ID)); err == nil {
		return fmt.Errorf("container %q already exists", c.ID)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}

	rootfsDst := filepath.Join(containerDir(c.ID), "rootfs")
	if err := os.MkdirAll(rootfsDst, 0755); err != nil {
		return err
	}
	if err := cp.Copy(rootfsSrc, rootfsDst); err != nil {
		return err
	}

	return launchChild(c, bundle, spec, opts.Memory, opts.CPUs, opts.Detach)
}

// launchChild spawns the "/proc/self/exe child" process for c, applies
// cgroup limits (if any), and either returns immediately (detach) or blocks
// until the process exits, recording its status/exit code either way.
// Shared by run() and StartContainer() (restarting a stopped container).
func launchChild(c *Container, bundle string, spec *specs.Spec, memory string, cpus float64, detach bool) error {
	cmd := exec.Command(
		"/proc/self/exe",
		"child",
		bundle,
		c.ID,
	)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: namespaceFlags(spec),
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	c.PID = cmd.Process.Pid
	c.markRunning()
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}

	if memory != "" || cpus > 0 {
		if err := SetupCgroup(c.ID, c.PID, memory, cpus); err != nil {
			return err
		}
	}

	if detach {
		return nil
	}

	waitErr := cmd.Wait()
	c.markExited()
	if cmd.ProcessState != nil {
		c.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}
	if err := CleanupCgroup(c.ID); err != nil {
		return err
	}
	return waitErr
}

func namespaceFlags(spec *specs.Spec) uintptr {
	var flags uintptr
	for _, ns := range spec.Linux.Namespaces {
		switch ns.Type {
		case specs.PIDNamespace:
			flags |= syscall.CLONE_NEWPID
		case specs.UTSNamespace:
			flags |= syscall.CLONE_NEWUTS
		case specs.MountNamespace:
			flags |= syscall.CLONE_NEWNS
		case specs.NetworkNamespace:
			flags |= syscall.CLONE_NEWNET
		}
	}
	return flags
}

func child() error {
	if len(os.Args) < 4 {
		return fmt.Errorf("child requires bundle path and container id")
	}

	bundle := os.Args[2]
	containerID := os.Args[3]
	spec, err := loadSpec(bundle)
	if err != nil {
		return err
	}

	// Hostname (OCI)
	if spec.Hostname != "" {
		if err := syscall.Sethostname([]byte(spec.Hostname)); err != nil {
			return err
		}
	}

	// Root filesystem: the per-container copy made by run(), not the bundle's own rootfs
	rootfs := filepath.Join(containerDir(containerID), "rootfs")

	container, err := readContainer(containerID)
	if err != nil {
		return err
	}
	if err := mountVolumes(rootfs, container.Volumes); err != nil {
		return err
	}

	// Open the log file while the host path is still resolvable (pre-pivot).
	// The fd stays valid across pivot_root since it isn't path-based.
	logFile, err := os.OpenFile(filepath.Join(containerDir(containerID), "logs.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	// If OCI root is readonly, remount rootfs as read-only
	if spec.Root.Readonly {
		if err := syscall.Mount(
			rootfs,
			rootfs,
			"",
			syscall.MS_BIND|syscall.MS_REC,
			"",
		); err != nil {
			return err
		}

		if err := syscall.Mount(
			rootfs,
			rootfs,
			"",
			syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY,
			"",
		); err != nil {
			return err
		}
	}
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		return err
	}
	if err := pivotRoot(rootfs); err != nil {
		return err
	}
	if err := mountAll(spec); err != nil {
		return err
	}
	if err := setupNetwork(); err != nil {
		return err
	}
	if err := setupDevices(); err != nil {
		return err
	}
	_ = os.Remove("/dev/ptmx")
	if err := os.Symlink("pts/ptmx", "/dev/ptmx"); err != nil {
		return err
	}

	// OCI process
	proc := spec.Process
	cmd := exec.Command(proc.Args[0], proc.Args[1:]...)
	cmd.Env = proc.Env
	cmd.Dir = proc.Cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, logFile)
	cmd.Stderr = io.MultiWriter(os.Stderr, logFile)

	if err := syscall.Setgid(int(proc.User.GID)); err != nil {
		return err
	}

	if err := syscall.Setuid(int(proc.User.UID)); err != nil {
		return err
	}

	// PID 1 reaping
	go func() {
		for {
			var status syscall.WaitStatus
			_, err := syscall.Wait4(-1, &status, 0, nil)
			if err != nil {
				// No more children → exit loop
				return
			}
		}
	}()

	return cmd.Run()
}

func pivotRoot(rootfs string) error {
	// Make sure rootfs is a mount point
	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return err
	}

	putOld := filepath.Join(rootfs, ".oldroot")
	if err := os.MkdirAll(putOld, 0700); err != nil {
		return err
	}

	// Actual pivot_root syscall
	if err := syscall.PivotRoot(rootfs, putOld); err != nil {
		return err
	}

	// Change working dir to new root
	if err := os.Chdir("/"); err != nil {
		return err
	}

	putOld = "/.oldroot"

	// Unmount old root
	if err := syscall.Unmount(putOld, syscall.MNT_DETACH); err != nil {
		return err
	}

	return os.RemoveAll(putOld)
}

func mountAll(spec *specs.Spec) error {
	for _, m := range spec.Mounts {
		dest := m.Destination

		if err := os.MkdirAll(dest, 0755); err != nil {
			return err
		}

		var flags uintptr
		var data []string

		for _, opt := range m.Options {
			switch opt {
			case "nosuid":
				flags |= syscall.MS_NOSUID
			case "noexec":
				flags |= syscall.MS_NOEXEC
			case "nodev":
				flags |= syscall.MS_NODEV
			default:
				data = append(data, opt)
			}
		}

		dataStr := strings.Join(data, ",")

		if err := syscall.Mount(
			m.Source,
			dest,
			m.Type,
			flags,
			dataStr,
		); err != nil {
			return fmt.Errorf("mount %s failed: %v", dest, err)
		}
	}
	return nil
}

func ensureCharDevice(path string, mode uint32, dev int) error {
	if err := syscall.Mknod(path, mode, dev); err == nil {
		return nil
	} else if err != syscall.EEXIST {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && info.Mode()&os.ModeCharDevice != 0 && uint64(stat.Rdev) == uint64(dev) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove existing %s: %w", path, err)
	}
	return syscall.Mknod(path, mode, dev)
}

// setupNetwork brings up the loopback interface inside a fresh network
// namespace. Containers get only "lo" by default — no host interfaces,
// no external connectivity. Bridge/veth networking is left for later.
func setupNetwork() error {
	if _, err := exec.LookPath("ip"); err != nil {
		return nil
	}
	return exec.Command("ip", "link", "set", "lo", "up").Run()
}

func setupDevices() error {
	devices := []struct {
		path  string
		major uint32
		minor uint32
	}{
		{"/dev/null", 1, 3},
		{"/dev/zero", 1, 5},
		{"/dev/random", 1, 8},
		{"/dev/urandom", 1, 9},
		{"/dev/tty", 5, 0},
	}
	for _, d := range devices {
		if err := ensureCharDevice(d.path, syscall.S_IFCHR|0666, int(d.major<<8|d.minor)); err != nil {
			return fmt.Errorf("setup %s: %w", d.path, err)
		}
	}
	return nil
}

// -------------------------------------------------
// SYSTEMD DELEGATION LOGIC
// -------------------------------------------------

// needDelegate checks whether:
// - we are running under systemd
// - and we need a delegated cgroup
// so that this process can manage its own cgroups
func needDelegate() bool {
	// If already delegated, no need again
	if os.Getenv("RUNC_DELEGATED") == "1" {
		return false
	}

	// If systemd-run is not available, cannot delegate
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}

	// Check if PID 1 is systemd
	p1, _ := os.ReadFile("/proc/1/comm")
	if !bytes.HasPrefix(p1, []byte("systemd")) {
		return false
	}

	// Check if we are in init.scope (not delegated)
	self, _ := os.ReadFile("/proc/self/cgroup")
	return bytes.Contains(self, []byte("/init.scope"))
}

// reexecInDelegate re-launches this program using systemd-run
// with cgroup delegation enabled.
func reexecInDelegate(pidsMax int) error {
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}

	// systemd-run arguments:
	// --scope            -> run in a transient scope unit
	// Delegate=yes      -> allow this process to manage cgroups
	// TasksMax          -> limit number of processes
	// RUNC_DELEGATED=1  -> prevent infinite recursion
	args := []string{
		"--scope",
		"-p", "Delegate=yes",
		"-p", fmt.Sprintf("TasksMax=%d", pidsMax),
		"--setenv=RUNC_DELEGATED=1",
		"--quiet",
		selfExe,
	}

	// Pass original arguments (run/child + command)
	args = append(args, os.Args[1:]...)

	// systemd-run's transient scope hits the fd soft limit on some hosts,
	// which makes journald broadcast a "Failed to allocate manager object"
	// error to every terminal. Raising the soft limit to the hard limit
	// before spawning it avoids that (container still starts fine either way).
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err == nil {
		rlim.Cur = rlim.Max
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rlim)
	}

	cmd := exec.Command("systemd-run", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	// Exit original process — new delegated one continues
	os.Exit(0)
	return nil
}
