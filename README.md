# Mini ContainerD

Mini ContainerD is an educational project where I’m building a minimal container runtime from scratch in Go. The goal is to understand how containers actually work under the hood — beyond Docker abstractions — by directly working with Linux primitives like namespaces, mounts, cgroups, and process isolation.

This is not meant to be production-ready. It’s a learning project focused on exploring systems programming and container internals.

---

## What it currently does

- Isolated environments using Linux namespaces (PID, UTS, mount, network)
- Custom hostname inside the container
- Root filesystem switching via `pivot_root`
- Loading images from `.tar`/`.tar.gz` rootfs bundles, with a per-container
  rootfs copy so containers never share or contaminate each other's filesystem
- cgroup v2 memory/CPU limits (`--memory`, `--cpus`)
- Volume bind mounts (`--volume host:container[:ro]`)
- stdout/stderr capture to a per-container log file
- Full lifecycle management: `run` (foreground or `--detach`), `start`,
  `stop` (SIGTERM then SIGKILL after a timeout), `kill`
- `exec` into a running container's existing namespaces
- A minimal shell environment using BusyBox

Running a container drops you into a shell that is isolated from the host’s process tree, filesystem, and network stack.

---

## Commands

```
mini-containerd run [options] <bundle>       run a container from a bundle
mini-containerd load, import <name>          load an image from a tar/tar.gz rootfs bundle
mini-containerd images                       list loaded images
mini-containerd ps                           list containers
mini-containerd logs <container>             show a container's captured stdout/stderr
mini-containerd start <container>            restart a stopped container
mini-containerd stop <container>             gracefully stop a running container
mini-containerd kill <container>             force-kill a running container
mini-containerd exec <container> <command>   run a command inside a running container
```

`run` options:

```
--image     name of a loaded image to use as rootfs (see load/images)
--name      container name (defaults to a random id)
--memory    memory limit, e.g. 256m, 1g (cgroup v2 memory.max)
--cpus      cpu limit in cores, e.g. 0.5 (cgroup v2 cpu.max)
--volume    bind mount host:container[:ro], repeatable
--detach    run the container in the background
```

`--debug` (global) shows full error detail instead of a friendly message.

Run `mini-containerd <command> --help` for a command's full option list.

---

## Example

```sh
sudo ./mini-containerd load --file=busybox-rootfs.tar.gz busybox
sudo ./mini-containerd run --image=busybox --name=c1 bundle
```

Inside the container:

```sh
/ # ps
PID   USER     TIME  COMMAND
1     root     0:00 /proc/self/exe child bundle c1
6     root     0:00 /bin/sh
9     root     0:00 ps
```

Only container processes are visible, which confirms PID namespace isolation. Running a second container from the same image (`--name=c2`) gets its own rootfs copy — files written in `c1` never show up in `c2`.

---

## Project Structure

```
.
├── main.go          # CLI wiring, run/child lifecycle, namespaces, mounts
├── container.go     # Container struct, disk persistence, ListContainers
├── image.go         # load/import, tar extraction, manifest.json
├── cgroup.go        # cgroup v2 memory/cpu limits
├── volume.go        # --volume parsing and bind mounts
├── logs.go          # stdout/stderr capture and `logs` command
├── lifecycle.go     # start/stop/kill
├── exec.go          # exec into a running container's namespaces
├── bundle/
│   ├── config.json  # OCI-like runtime configuration
│   └── rootfs/      # container root filesystem (gitignored, built from BusyBox)
```

---

## How it works (high level)

The runtime follows a simplified version of how tools like containerd/runc operate:

1. Load container configuration (`config.json`) and resolve a rootfs (bundle-local or a loaded `--image`)
2. Copy that rootfs into a per-container directory under `/var/lib/mini-containerd/containers/<id>/`
3. Re-execute itself as a child process with new namespaces (PID, UTS, mount, network)
4. Set up the root filesystem using `pivot_root` into the per-container copy
5. Mount required filesystems (`/proc`, `/dev/pts`, volumes, etc.) and bring up loopback networking
6. Apply cgroup v2 limits, if requested
7. Execute the container process, capturing its output to a log file

The parent process acts as a launcher, while the child process becomes PID 1 inside the container. `exec`, `stop`, `kill`, and `start` all act on a container by reading its persisted state (`config.json`) rather than needing the original launching process still around.

---

## Running the project

### Requirements

- Linux (tested on Fedora and Debian)
- Go installed
- Root privileges (required for namespaces, mounts, and cgroups)
- `systemd` cgroup delegation is handled automatically if needed

### Build

```bash
go build -o mini-containerd
```

### Run

```bash
sudo ./mini-containerd run bundle
```

---

## Testing

Unit tests cover the pure-logic helpers that don't need root or real namespaces
(image manifest read/write, tar path-traversal guards, volume/memory spec
parsing, container state transitions). They don't exercise namespace/mount/
cgroup behavior itself — that still needs manual testing as root, e.g. the
`run --image=... --name=...` isolation check described above.

```bash
go test ./...
```

CI (`.github/workflows/ci.yml`) runs `go build`, `go vet`, and `go test` on
every push and pull request.

---

## Root filesystem

This project currently uses a minimal BusyBox-based root filesystem.
A static BusyBox binary is used to avoid dependency issues inside the container.

---

## Current limitations

This is still a work in progress. Some known limitations:

- `/dev` implementation covers only the common device nodes (null, zero, random, urandom, tty)
- Networking is loopback-only — no veth/bridge connectivity to the host or outside world yet
- `exec`'s mount-namespace join relies on a locked-OS-thread `setns`, which is simpler but less
  robust than runc's C-constructor (`nsenter`) approach
- `start` (restarting a stopped container) doesn't reapply the original run's `--memory`/`--cpus`
  limits, since they aren't persisted on the container yet
- `logs --follow` (real-time streaming) isn't implemented
- No image layering — a loaded image is always a single flat layer

---

## Why this project exists

This project is part of my effort to get deeper into systems programming and distributed systems tooling. Instead of treating containers as black boxes, I wanted to understand:

- How process isolation actually works
- How filesystems are switched and mounted
- How runtimes like runc/containerd are structured

---

## Related projects

Some of my other educational projects:

- stream — https://github.com/xonas1101/stream
- logger-controller — https://github.com/xonas1101/logger-controller

---

## Status

Core runtime feature set (images, cgroups, volumes, logging, lifecycle, exec) implemented and tested.
Still actively improving — see open issues for what's next.
