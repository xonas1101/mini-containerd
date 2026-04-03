# Mini ContainerD

Mini ContainerD is an educational project where I’m building a minimal container runtime from scratch in Go. The goal is to understand how containers actually work under the hood — beyond Docker abstractions — by directly working with Linux primitives like namespaces, mounts, and process isolation.

This is not meant to be production-ready. It’s a learning project focused on exploring systems programming and container internals.

---

## What it currently does

At its current stage, the runtime can:

- Create isolated environments using Linux namespaces (PID, UTS, mount)
- Set a custom hostname inside the container
- Switch the root filesystem using `pivot_root`
- Execute a process inside the container
- Mount basic filesystems like `/proc`
- Provide a minimal shell environment using BusyBox

Running a container drops you into a shell that is isolated from the host’s process tree and filesystem.

---

## Example

Inside the container:

```sh
/ # ps
PID   USER     TIME  COMMAND
1     root     0:00 /proc/self/exe child bundle
6     root     0:00 /bin/sh
9     root     0:00 ps
```

Only container processes are visible, which confirms PID namespace isolation.

---

## Project Structure

```
.
├── main.go        # Core runtime implementation
├── bundle/
│   ├── config.json  # OCI-like runtime configuration
│   └── rootfs/      # Container root filesystem
```

---

## How it works (high level)

The runtime follows a simplified version of how tools like containerd/runc operate:

1. Load container configuration (`config.json`)
2. Re-execute itself as a child process
3. Create new namespaces (PID, UTS, mount)
4. Set up the root filesystem using `pivot_root`
5. Mount required filesystems (`/proc`, etc.)
6. Execute the container process

The parent process acts as a launcher, while the child process becomes PID 1 inside the container.

---

## Running the project

### Requirements

- Linux (tested on Fedora)
- Go installed
- Root privileges (required for namespaces and mounts)

### Build

```bash
go build -o mini-containerd
```

### Run

```bash
sudo ./mini-containerd run bundle
```

---

## Root filesystem

This project currently uses a minimal BusyBox-based root filesystem.  
A static BusyBox binary is used to avoid dependency issues inside the container.

---

## Current limitations

This is still a work in progress. Some known limitations:

- `/dev` is not fully implemented yet
- No cgroup support (no CPU/memory limits)
- No networking isolation
- Minimal error handling in some areas
- Basic process lifecycle handling

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

Early prototype, but functional.  
Still actively improving and adding features.
This is a working demo of containerd container runtime