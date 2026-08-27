# tauri.Dockerfile — Dark Factory build/test container for Tauri 2 projects
#
# PURPOSE: Build + test only. This container does NOT run the GUI application.
# Tauri's desktop GUI requires a display server (X11/Wayland) that containers
# don't have. What this container does:
#   - cargo build (Tauri + Rust backend)
#   - cargo test (Rust unit and integration tests)
#   - npx vitest run (frontend unit tests)
#   - npx biome check (lint + format)
#
# Playwright / E2E tests require a headless browser. Options:
#   - Add playwright + chromium to this image (heavy, ~1.5GB extra)
#   - Run Playwright from the host against a dev server
#   - Use a separate playwright service in docker-compose
#   Current recommendation: run Playwright host-side until E2E is a gate requirement.

FROM rust:1.82-bookworm

# ── System dependencies ────────────────────────────────────────────────────────
# Tauri 2 requires these at build time on Linux.
# libwebkit2gtk-4.1-dev is the Tauri 2 variant (4.0 is Tauri 1).
RUN apt-get update && apt-get install -y --no-install-recommends \
    # Build toolchain
    pkg-config \
    build-essential \
    # SSL (reqwest / native-tls)
    libssl-dev \
    # GTK + WebKit (Tauri 2 system webview)
    libgtk-3-dev \
    libwebkit2gtk-4.1-dev \
    # SVG / icon rasterization
    librsvg2-dev \
    # Needed by webkit2gtk
    libayatana-appindicator3-dev \
    # curl for Node.js install script
    curl \
    ca-certificates \
    # Clean up apt cache to keep image small
 && rm -rf /var/lib/apt/lists/*

# ── Node.js 22 LTS ────────────────────────────────────────────────────────────
# Use the official NodeSource setup script for the LTS channel.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*

# ── Rust targets ──────────────────────────────────────────────────────────────
# x86_64-unknown-linux-gnu is already present in the base image.
# Add wasm32 for any frontend Rust (optional; remove if not needed).
RUN rustup target add \
    x86_64-unknown-linux-gnu \
    wasm32-unknown-unknown

# ── cargo-tauri CLI ───────────────────────────────────────────────────────────
# Install the Tauri CLI as a cargo binary. Pinned to 2.x — update as needed.
# This is the build tool, not the runtime; it runs `tauri build` / `tauri dev`.
RUN cargo install tauri-cli --version "^2" --locked

# ── Working directory ─────────────────────────────────────────────────────────
WORKDIR /app

# ── Default command ───────────────────────────────────────────────────────────
# Runs the full quality gate: lint → test → build.
# Override with `docker run ... make <target>` for a specific step.
CMD ["make", "gate"]
