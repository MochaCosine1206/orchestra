package orchestrator

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// DetectTestCommand examines the project directory and returns the appropriate
// build+test command for the conductor's test_cmd field.
func DetectTestCommand(projectPath string) string {
	// Check for Tauri project (has both src-tauri/Cargo.toml AND package.json)
	// Tauri's `npm run build` triggers full bundling (DMG/MSI) which fails without
	// codesigning. Use cargo check + vite build instead.
	if fileExists(filepath.Join(projectPath, "src-tauri", "Cargo.toml")) &&
		fileExists(filepath.Join(projectPath, "package.json")) {
		return "cd src-tauri && cargo check && cargo test && cd .. && npx tsc --noEmit && npx vite build"
	}

	// Check for Node.js (package.json)
	if fileExists(filepath.Join(projectPath, "package.json")) {
		return "npm install && npm run build && npm test"
	}

	// Check for Go (go.mod)
	if fileExists(filepath.Join(projectPath, "go.mod")) {
		return "go build ./... && go test ./..."
	}

	// Check for Rust (Cargo.toml) — standalone Rust, not Tauri
	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		return "cargo build && cargo test"
	}

	// Check for Python (pyproject.toml or setup.py)
	if fileExists(filepath.Join(projectPath, "pyproject.toml")) || fileExists(filepath.Join(projectPath, "setup.py")) {
		return "pip install -e . && pytest"
	}

	// Check for Makefile with 'test' target
	makefilePath := filepath.Join(projectPath, "Makefile")
	if fileExists(makefilePath) && makefileHasTestTarget(makefilePath) {
		return "make test"
	}

	return ""
}

// DetectInstallCommand examines the project directory and returns the command(s)
// needed to install all dependencies. Runs BEFORE gate test commands.
// Handles multiple ecosystems in the same project (e.g., Tauri = Rust + npm).
func DetectInstallCommand(projectPath string) string {
	var cmds []string

	// Check for Rust (Cargo.toml) — may be in src-tauri/ for Tauri projects
	cargoLocations := []string{
		filepath.Join(projectPath, "Cargo.toml"),
		filepath.Join(projectPath, "src-tauri", "Cargo.toml"),
	}
	for _, cargoPath := range cargoLocations {
		if fileExists(cargoPath) {
			dir := filepath.Dir(cargoPath)
			relDir, _ := filepath.Rel(projectPath, dir)
			if relDir == "." {
				cmds = append(cmds, "cargo fetch")
			} else {
				cmds = append(cmds, "cd "+relDir+" && cargo fetch && cd ..")
			}
			break
		}
	}

	// Check for Node.js (package.json)
	if fileExists(filepath.Join(projectPath, "package.json")) {
		cmds = append(cmds, "npm install")
	}

	// Check for Python
	if fileExists(filepath.Join(projectPath, "pyproject.toml")) {
		cmds = append(cmds, "pip install -e .")
	} else if fileExists(filepath.Join(projectPath, "requirements.txt")) {
		cmds = append(cmds, "pip install -r requirements.txt")
	} else if fileExists(filepath.Join(projectPath, "setup.py")) {
		cmds = append(cmds, "pip install -e .")
	}

	// Check for Go
	if fileExists(filepath.Join(projectPath, "go.mod")) {
		cmds = append(cmds, "go mod download")
	}

	// Check for Ruby
	if fileExists(filepath.Join(projectPath, "Gemfile")) {
		cmds = append(cmds, "bundle install")
	}

	if len(cmds) == 0 {
		return ""
	}
	return strings.Join(cmds, " && ")
}

// FixDevConfig detects the project's framework and fixes missing configuration
// that agents commonly miss. Called after foundation phase gate passes.
// Grows over time as we encounter new framework-specific issues.
func FixDevConfig(projectPath string) []string {
	var fixes []string

	// Tauri 2: beforeDevCommand and beforeBuildCommand
	tauriConf := filepath.Join(projectPath, "src-tauri", "tauri.conf.json")
	if fileExists(tauriConf) {
		data, err := os.ReadFile(tauriConf)
		if err == nil {
			content := string(data)
			changed := false

			// Add beforeDevCommand if missing
			if !strings.Contains(content, "beforeDevCommand") && strings.Contains(content, "devUrl") {
				content = strings.Replace(content, `"devUrl"`, `"beforeDevCommand": "npm run dev",
    "beforeBuildCommand": "npm run build",
    "devUrl"`, 1)
				changed = true
				fixes = append(fixes, "tauri.conf.json: added beforeDevCommand and beforeBuildCommand")
			}

			if changed {
				os.WriteFile(tauriConf, []byte(content), 0o644)
			}
		}

		// Vite port matching: if tauri.conf.json specifies a devUrl port, ensure vite.config matches
		if data, err := os.ReadFile(tauriConf); err == nil {
			content := string(data)
			// Extract port from devUrl
			port := ""
			if idx := strings.Index(content, "devUrl"); idx >= 0 {
				sub := content[idx:]
				for _, p := range []string{"1420", "5173", "3000", "8080"} {
					if strings.Contains(sub[:min(len(sub), 200)], p) {
						port = p
						break
					}
				}
			}
			if port != "" && port != "5173" { // 5173 is vite default, no fix needed
				viteConf := filepath.Join(projectPath, "vite.config.ts")
				if fileExists(viteConf) {
					viteData, _ := os.ReadFile(viteConf)
					viteContent := string(viteData)
					if !strings.Contains(viteContent, "port:") && !strings.Contains(viteContent, "port :") {
						// Add port to server config
						if strings.Contains(viteContent, "strictPort") {
							viteContent = strings.Replace(viteContent, "strictPort: true",
								"port: "+port+",\n    strictPort: true", 1)
						} else if strings.Contains(viteContent, "server:") || strings.Contains(viteContent, "server :") {
							viteContent = strings.Replace(viteContent, "server: {",
								"server: {\n    port: "+port+",", 1)
						}
						os.WriteFile(viteConf, []byte(viteContent), 0o644)
						fixes = append(fixes, "vite.config.ts: added port "+port+" to match tauri.conf.json devUrl")
					}
				}
			}
		}
	}

	return fixes
}

// fileExists checks if a file exists at the given path.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// makefileHasTestTarget scans a Makefile for a 'test' target.
func makefileHasTestTarget(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Match "test:" or "test: <deps>" at the start of a line
		if strings.HasPrefix(line, "test:") || strings.HasPrefix(line, "test :") {
			return true
		}
	}
	return false
}
