//! Supplider Tauri 2 desktop shell.
//!
//! This binary contains NO business logic — it is pure infrastructure
//! (one of the three allowed tier-difference layers):
//!
//! 1. Launch the Go sidecar (`suppliderd-<target-triple>`, produced by
//!    `scripts/build-sidecar.sh`) as a Tauri sidecar process, passing a
//!    per-user data directory (SQLite db + attachments).
//! 2. Wait for its `/readyz` endpoint so the UI never sticks on
//!    "后端未连接".
//! 3. Supervise the sidecar: if it dies mid-session, respawn it (bounded
//!    backoff + crash-loop give-up) so the window self-heals without the
//!    user double-clicking the app again.
//! 4. Kill the sidecar on app exit (no orphan daemons).
//!
//! The React frontend is embedded by Tauri (`frontendDist`) and calls the
//! sidecar at http://127.0.0.1:7612; the sidecar answers CORS for the
//! Tauri origin (tauri://localhost / tauri.localhost).

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use tauri::async_runtime::Receiver;
use tauri::{AppHandle, Manager, RunEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// Loopback address the sidecar listens on. Mirrors the daemon default
/// and the VITE_API_BASE baked into the Tauri frontend bundle.
const SIDECAR_ADDR: &str = "127.0.0.1:7612";

/// Delay before a respawn. Keeps a crash loop from hot-spinning.
const RESPAWN_DELAY: Duration = Duration::from_secs(1);
/// A sidecar that lives at least this long counts as stable and resets
/// the consecutive-fast-crash counter.
const STABLE_LIFETIME: Duration = Duration::from_secs(10);
/// Consecutive deaths before reaching STABLE_LIFETIME after which the
/// supervisor gives up (the frontend's reconnect screen then prompts
/// the user to restart the app — better than respawning forever).
const MAX_FAST_CRASHES: u32 = 5;

/// Shared sidecar process state. Held in an `Arc` so both the supervisor
/// thread and the Tauri exit handler can reach the live child handle and
/// the shutdown flag.
struct SidecarInner {
    child: Mutex<Option<CommandChild>>,
    /// Set when the app is exiting: the Terminated event caused by our
    /// own kill() must never trigger a respawn.
    shutting_down: AtomicBool,
}

impl SidecarInner {
    /// Mark the app as shutting down and kill the live sidecar. Idempotent:
    /// the second caller finds `child` already taken. Used by both the
    /// window-close handler and the SIGTERM/SIGINT watcher.
    fn shutdown(&self) {
        self.shutting_down.store(true, Ordering::SeqCst);
        if let Ok(mut guard) = self.child.lock() {
            if let Some(child) = guard.take() {
                let _ = child.kill();
            }
        }
    }
}

/// Tauri-managed wrapper (`app.state::<SidecarState>()`).
struct SidecarState(Arc<SidecarInner>);

fn main() {
    // Block SIGTERM/SIGINT before the Tauri runtime spawns threads so the
    // signal watcher thread can sigwait() them (no-op off Unix). Without
    // this, task-manager kills / session logout use the default action and
    // orphan the sidecar because ExitRequested never fires.
    signals::block_exit_signals();

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            // Per-user data directory (OS-appropriate: %APPDATA%, ~/Library,
            // ~/.config). Created if missing.
            let data_dir = app
                .path()
                .app_data_dir()
                .expect("resolve app data directory");
            std::fs::create_dir_all(&data_dir).expect("create app data directory");

            // Spawn the Go sidecar. Tauri resolves
            // binaries/suppliderd-<target-triple>[.exe] from externalBin.
            // A missing binary is a packaging error — fail setup loudly.
            let (rx, child) = spawn_sidecar(app.handle(), &data_dir)
                .expect("sidecar binary missing — run scripts/build-sidecar.sh");

            let inner = Arc::new(SidecarInner {
                child: Mutex::new(Some(child)),
                shutting_down: AtomicBool::new(false),
            });
            app.manage(SidecarState(inner.clone()));

            // Signal-driven shutdown (task-manager kill / logout): same
            // cleanup as the window-close path, on a dedicated thread.
            signals::spawn_watcher(inner.clone());

            // Supervisor owns the event receiver for every sidecar
            // generation, forwards stdout/stderr, and respawns on
            // unexpected termination.
            let app_handle = app.handle().clone();
            thread::spawn(move || {
                supervise(app_handle, data_dir, inner, rx);
            });

            // Poll readiness off the main thread; the window opens
            // immediately and the frontend retries until the API answers.
            spawn_ready_watch();

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            // Stop the sidecar when the app exits. Set the flag BEFORE
            // kill() so the supervisor reads the resulting Terminated
            // event as intentional and does not respawn.
            if let RunEvent::ExitRequested { .. } = event {
                if let Some(state) = app.try_state::<SidecarState>() {
                    state.0.shutdown();
                }
            }
        });
}

/// Spawn one generation of the sidecar, returning its event receiver and
/// child handle. Tauri resolves the platform-suffixed binary from
/// `externalBin` (binaries/suppliderd-<target-triple>[.exe]).
fn spawn_sidecar(
    app: &AppHandle,
    data_dir: &Path,
) -> Result<(Receiver<CommandEvent>, CommandChild), Box<dyn std::error::Error>> {
    let (rx, child) = app
        .shell()
        .sidecar("suppliderd")?
        .args([
            "--addr",
            SIDECAR_ADDR,
            "--data-dir",
            data_dir.to_str().expect("data dir is valid UTF-8"),
        ])
        .spawn()?;
    Ok((rx, child))
}

/// Log-only readiness poller (the frontend gate is the real readiness
/// mechanism). One-shot: returns once `/readyz` answers or after ~20s.
fn spawn_ready_watch() {
    thread::spawn(|| {
        let url = format!("http://{}/readyz", SIDECAR_ADDR);
        for _ in 0..100 {
            if reqwest::blocking::get(&url).is_ok() {
                println!("[shell] sidecar ready on {}", SIDECAR_ADDR);
                return;
            }
            thread::sleep(Duration::from_millis(200));
        }
        eprintln!("[shell] sidecar did not become ready within 20s");
    });
}

/// Own the sidecar event stream across respawn generations. Each
/// generation gets a fresh receiver; on Terminated the supervisor
/// decides whether to respawn, hand over, or give up.
fn supervise(
    app: AppHandle,
    data_dir: PathBuf,
    inner: Arc<SidecarInner>,
    mut rx: Receiver<CommandEvent>,
) {
    let mut started_at = Instant::now();
    let mut fast_crashes = 0u32;

    'generations: loop {
        let mut respawned = false;

        while let Some(event) = rx.blocking_recv() {
            match event {
                CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
                    eprint!("[suppliderd] {}", String::from_utf8_lossy(&bytes));
                }
                CommandEvent::Terminated(payload) => {
                    eprintln!(
                        "[suppliderd] terminated: exit={:?} signal={:?}",
                        payload.code, payload.signal
                    );

                    // Our own exit kill.
                    if inner.shutting_down.load(Ordering::SeqCst) {
                        break 'generations;
                    }

                    // Clean exit while a healthy sidecar still answers:
                    // the single-instance handover — another instance of
                    // this app already owns the port (e.g. app launched
                    // twice). It serves the UI; do not fight it.
                    if payload.code == Some(0) && readyz_ok() {
                        eprintln!(
                            "[shell] another healthy sidecar owns {}; not respawning",
                            SIDECAR_ADDR
                        );
                        break 'generations;
                    }

                    // Crash-loop guard: count short-lived generations.
                    if started_at.elapsed() >= STABLE_LIFETIME {
                        fast_crashes = 0;
                    } else {
                        fast_crashes += 1;
                    }
                    if fast_crashes >= MAX_FAST_CRASHES {
                        eprintln!(
                            "[shell] sidecar crashed {} times within {:?}; giving up — restart the app",
                            fast_crashes, STABLE_LIFETIME
                        );
                        break 'generations;
                    }

                    eprintln!(
                        "[shell] respawning sidecar in {:?} (attempt {}/{})",
                        RESPAWN_DELAY,
                        fast_crashes + 1,
                        MAX_FAST_CRASHES
                    );
                    thread::sleep(RESPAWN_DELAY);
                    if inner.shutting_down.load(Ordering::SeqCst) {
                        break 'generations;
                    }

                    match spawn_sidecar(&app, &data_dir) {
                        Ok((new_rx, new_child)) => {
                            // Publish under the lock with a final shutdown
                            // re-check: if ExitRequested ran while we were
                            // spawning, kill this orphan immediately
                            // instead of leaking it past app exit.
                            let mut guard = inner.child.lock().unwrap();
                            if inner.shutting_down.load(Ordering::SeqCst) {
                                drop(guard);
                                let _ = new_child.kill();
                                break 'generations;
                            }
                            *guard = Some(new_child);
                            drop(guard);
                            rx = new_rx;
                            started_at = Instant::now();
                            respawned = true;
                            spawn_ready_watch();
                            break; // restart the receive loop on the new receiver
                        }
                        Err(e) => {
                            eprintln!("[shell] respawn failed: {e}; giving up — restart the app");
                            break 'generations;
                        }
                    }
                }
                _ => {}
            }
        }

        // Receiver closed without a respawn decision (senders dropped,
        // normally via shutdown) — supervision ends.
        if !respawned {
            break;
        }
    }
}

/// One cheap readiness probe, used only to detect the single-instance
/// handover case.
fn readyz_ok() -> bool {
    let client = match reqwest::blocking::Client::builder()
        .timeout(Duration::from_millis(500))
        .build()
    {
        Ok(c) => c,
        Err(_) => return false,
    };
    client
        .get(format!("http://{}/readyz", SIDECAR_ADDR))
        .send()
        .is_ok()
}

/// Unix signal handling: make SIGTERM/SIGINT run the same sidecar cleanup
/// as window close, instead of using the default process-kill action that
/// would orphan the Go sidecar. Signals are blocked process-wide at the
/// very start of main; a dedicated thread sigwait()s and performs cleanup.
#[cfg(unix)]
mod signals {
    use super::SidecarInner;
    use std::mem;
    use std::ptr;
    use std::sync::Arc;
    use std::thread;
    use std::time::Duration;

    pub fn block_exit_signals() {
        unsafe {
            let mut set: libc::sigset_t = mem::zeroed();
            libc::sigemptyset(&mut set);
            libc::sigaddset(&mut set, libc::SIGTERM);
            libc::sigaddset(&mut set, libc::SIGINT);
            libc::sigprocmask(libc::SIG_BLOCK, &set, ptr::null_mut());
        }
    }

    pub fn spawn_watcher(inner: Arc<SidecarInner>) {
        thread::spawn(move || {
            let mut sig: libc::c_int = 0;
            // With a valid, blocked signal set sigwait only really fails on
            // EINTR; just retry on any non-zero return.
            loop {
                let waited = unsafe {
                    let mut set: libc::sigset_t = mem::zeroed();
                    libc::sigemptyset(&mut set);
                    libc::sigaddset(&mut set, libc::SIGTERM);
                    libc::sigaddset(&mut set, libc::SIGINT);
                    libc::sigwait(&set, &mut sig)
                };
                if waited == 0 {
                    break;
                }
            }
            eprintln!("[shell] received signal {sig}, performing sidecar cleanup");
            inner.shutdown();
            // Let the child death be reaped before the parent goes away.
            thread::sleep(Duration::from_millis(150));
            std::process::exit(0);
        });
    }
}

#[cfg(not(unix))]
mod signals {
    use super::SidecarInner;
    use std::sync::Arc;

    /// No POSIX signals off Unix (Windows uses taskkill, which the OS
    /// handles by closing the window — the ExitRequested path).
    pub fn block_exit_signals() {}
    pub fn spawn_watcher(_inner: Arc<SidecarInner>) {}
}
