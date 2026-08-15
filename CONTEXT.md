# WinMon Domain Context & Architecture Glossary

## Domain Model & Concepts

### WinMon
WinMon is a Windows remote management agent controlled through a Telegram Bot. It operates in dual modes:
1. **Console Mode:** Runs as a standalone interactive process in the user session.
2. **Windows Service Mode (Session 0 + Session 1):** The background service runs in Session 0 as SYSTEM, while interactive desktop operations (screenshot, webcam, clipboard, toasts) are delegated to a persistent user agent in Session 1 over Named Pipe IPC (`\\.\pipe\WinMonIPC`).

### Host Action
A discrete unit of work executed against the Windows host operating system. Examples include capturing media (screenshot, webcam, screen recording, audio), inspecting telemetry (metrics, process table), manipulating UI/desktop (clipboard, brightness, wallpaper, hotkey injection, notifications), or managing processes.

### ActionExecutor (Deep Module)
The primary execution engine that hides Windows session boundaries, Named Pipe IPC serialization, temp file allocation, and process lifecycle behind a single, high-leverage interface:
`Execute(ctx context.Context, action Action) (Result, error)`

### Adapters at the Execution Seam
- **In-Process Adapter:** Executes host actions directly in the current process (used in Console Mode, during local execution, or inside Session 1).
- **IPC Adapter:** Marshals actions across `\\.\pipe\WinMonIPC` to the Session 1 persistent agent when running as a Session 0 service.
- **Auto Adapter:** Dynamically selects the appropriate adapter based on the runtime context (Service vs Console).
- **Mock Adapter:** An in-memory fake satisfying `ActionExecutor` for deterministic, zero-dependency unit tests.

### Telegram Bot Coordinator
The control plane module that handles Telegram updates, enforces numeric user ID authorization, parses commands, delegates execution to `ActionExecutor`, and renders structured results (photos, animations, audio, documents, markdown messages) back to the user.

---

## Architectural Principles & Seams

- **The Interface is the Test Surface:** Callers (the Bot Coordinator) and unit tests interact with the same `ActionExecutor` interface at the execution seam.
- **Locality:** Named Pipe lifecycle, temporary output cleanup, and Windows session routing concentrate inside the executor rather than leaking into bot command handlers.
- **Two Adapters = Real Seam:** The `ActionExecutor` interface is justified by real divergence: in-process direct execution vs Session-crossing Named Pipe IPC.
