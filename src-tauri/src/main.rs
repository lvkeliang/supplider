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
//! 3. Kill the sidecar on app exit (no orphan daemons).
//!
//! The React frontend is embedded by Tauri (`frontendDist`) and calls the
//! sidecar at http://127.0.0.1:7612; the sidecar answers CORS for the
//! Tauri origin (tauri://localhost / tauri.localhost).

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Mutex;
use std::{thread, time::Duration};

use tauri::{Manager, RunEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// Loopback address the sidecar listens on. Mirrors the daemon default
/// and the VITE_API_BASE baked into the Tauri frontend bundle.
const SIDECAR_ADDR: &str = "127.0.0.1:7612";

/// Holds the sidecar child process so the exit handler can kill it.
struct SidecarState(Mutex<Option<CommandChild>>);

fn main() {
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
            let sidecar = app
                .shell()
                .sidecar("suppliderd")
                .expect("sidecar binary missing — run scripts/build-sidecar.sh")
                .args([
                    "--addr",
                    SIDECAR_ADDR,
                    "--data-dir",
                    data_dir.to_str().expect("data dir is valid UTF-8"),
                ]);

            let (mut rx, child) = sidecar.spawn().expect("spawn sidecar");
            app.manage(SidecarState(Mutex::new(Some(child))));

            // Forward sidecar stdout/stderr to the Tauri console.
            thread::spawn(move || {
                while let Ok(Some(event)) = rx.blocking_recv() {
                    match event {
                        CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
                            eprint!("[suppliderd] {}", String::from_utf8_lossy(&bytes));
                        }
                        CommandEvent::Terminated(payload) => {
                            eprintln!("[suppliderd] terminated: exit={:?}", payload.code);
                            break;
                        }
                        _ => {}
                    }
                }
            });

            // Poll readiness off the main thread; the window opens
            // immediately and the frontend retries until the API answers.
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

            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            // Stop the sidecar when the app exits.
            if let RunEvent::ExitRequested { .. } = event {
                if let Some(state) = app.try_state::<SidecarState>() {
                    if let Ok(mut guard) = state.0.lock() {
                        if let Some(child) = guard.take() {
                            let _ = child.kill();
                        }
                    }
                }
            }
        });
}
