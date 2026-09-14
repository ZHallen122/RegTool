![banner](./assets/banner.jpg)

### Supported Software

**RegTool** supports a wide range of software registries and package managers to enhance your development workflow. Below is a comprehensive list of the software currently supported:

**Features:**

- **CLI and TUI**: Scriptable subcommands for automation, an interactive interface when you just want to click around.
- **No external commands**: RegTool edits `~/.npmrc`, `~/.yarnrc.yml`, `pip.conf`, `~/.gemrc`, the Go env file and `~/.cargo/config.toml` itself, so it works on a machine where those tools are not installed. Homebrew is the exception: it is configured through environment variables, so it still runs `brew`.
- **Transactional changes**: every file is snapshotted before it is touched and written atomically, so `regtool undo` puts any change back.
- **Dry runs**: `--dry-run` prints the unified diff of every file that would change, without writing anything.
- **Cross platform**: Linux, macOS and Windows; every path goes through `os.UserConfigDir` and `path/filepath`.

### Usage

Run `regtool` with no arguments to open the interactive interface. Everything it
can do is also available as a subcommand:

```sh
# show the registry every installed package manager currently points at
regtool status

# list the mirrors RegTool knows about, optionally for one package manager
regtool list
regtool list npm

# preview a change: prints the unified diff of every file it would rewrite
regtool use cn npm --dry-run

# point npm and pip at the China mirrors; with no app names, every installed app
regtool use cn npm pip
regtool use us

# list the snapshots taken before each change, and roll one back
regtool history
regtool undo
regtool undo 20260914T041530.123Z-3f9a1c

# record the current registries so a later change can be compared against them
regtool refresh

# print the version
regtool version
```

`status`, `list`, `use` and `history` accept `--json`, which writes the result to
stdout so it can be piped into `jq` or another tool. Errors go to stderr and the
process exits with status 1.

A real `use` run copies every file it is about to change into
`<user config dir>/regtool/history/<snapshot id>/` first and prints the snapshot
id. `regtool undo` restores the newest snapshot, or the one you name; the
restore is itself snapshotted, so an undo can be undone.

By supporting a wide range of software and registries, RegTool aims to streamline your development process and provide a seamless experience across different ecosystems.

### Supported Software Table

| Name       | Description                                                                              | Availability                                                        |
| ---------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| npm        | Manage Node.js packages and switch between public and private npm registries seamlessly. | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| Yarn       | Configure Yarn package manager registries, supporting both public and private packages.  | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| Docker     | Handle Docker image registries, including Docker Hub and private Docker registries.      | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Homebrew   | Manage Homebrew taps and repositories for macOS and Linux package installations. Still driven by running `brew`, because homebrew is configured through environment variables. | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| pip        | Configure and manage Python package indexes, including PyPI and private repositories.    | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| RubyGems   | Manage Ruby gems and configure sources for gem installations.                            | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| Maven      | Handle Java dependencies and configure Maven repositories.                               | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Gradle     | Manage Gradle repositories for Java projects.                                            | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Composer   | Handle PHP package management with Composer and configure repositories.                  | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| NuGet      | Manage .NET packages and configure NuGet repositories.                                   | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Cargo      | Handle Rust packages and configure Cargo registries.                                     | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| Go Modules | Manage Go packages and configure module proxies.                                         | ![green](https://img.shields.io/badge/status-available-brightgreen) |
| Helm       | Configure Helm chart repositories for Kubernetes applications.                           | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Conan      | Manage C/C++ packages and configure Conan repositories.                                  | ![red](https://img.shields.io/badge/status-unavailable-red)         |
| Pub        | Handle Dart packages and configure Pub repositories.                                     | ![red](https://img.shields.io/badge/status-unavailable-red)         |

RegTool aims to streamline your development process and provide a seamless experience across different ecosystems.

### Build Instructions

To build RegTool from source, follow these steps:

1. **Clone the repository:**

   ```sh
   git clone https://github.com/yourusername/regtool.git
   cd regtool
   ```

2. **Build the project:**

   ```sh
   make build
   ```

3. **Run RegTool:**
   ```sh
   ./regtool
   ```

### Contribution

We welcome contributions! If you would like to contribute to this project, please follow the guidelines in our [CONTRIBUTION.md](./CONTRIBUTION.md).

Thank you for your interest in contributing to RegTool. Your contributions are greatly appreciated.
