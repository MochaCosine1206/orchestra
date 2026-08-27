package dashboard

import "fmt"

// RoleAvatar maps agent roles to their Noto WebP emoji filenames.
var RoleAvatar = map[string]string{
	"architect":   "triangular-ruler.webp",
	"implementer": "robot-face.webp",
	"reviewer":    "magnifying-glass.webp",
	"scout":       "compass.webp",
	"researcher":  "books.webp",
	"editor":      "writing-hand.webp",
	"conductor":   "musical-notes.webp",
}

// StateAvatar maps agent activity states to state-specific WebP filenames.
var StateAvatar = map[string]string{
	"working":   "hammer.webp",
	"reading":   "open-book.webp",
	"writing":   "laptop.webp",
	"testing":   "lightning.webp",
	"thinking":  "thinking-face.webp",
	"waiting":   "hourglass.webp",
	"success":   "party-popper.webp",
	"failed":    "dizzy-face.webp",
	"dead":      "skull.webp",
	"merging":   "handshake.webp",
	"reviewing": "monocle.webp",
}

// AvatarForAgent returns the appropriate WebP filename for the given role and status.
// State overrides take precedence over role avatars.
func AvatarForAgent(role, status string) string {
	if img, ok := StateAvatar[status]; ok {
		return img
	}
	if img, ok := RoleAvatar[role]; ok {
		return img
	}
	return "robot-face.webp"
}

// AvatarSrc returns the full static URL path for an agent's avatar.
func AvatarSrc(role, status string) string {
	return "/static/agents/" + AvatarForAgent(role, status)
}

// statusRingStyle holds pre-computed CSS for a status ring.
type statusRingStyle struct {
	BorderColor string
	BoxShadow   string
}

var statusRingStyles = map[string]statusRingStyle{
	"idle": {
		BorderColor: "var(--muted)",
		BoxShadow:   "none",
	},
	"working": {
		BorderColor: "var(--green)",
		BoxShadow:   "0 0 8px var(--green)",
	},
	"thinking": {
		BorderColor: "var(--yellow)",
		BoxShadow:   "0 0 8px var(--yellow)",
	},
	"waiting": {
		BorderColor: "var(--yellow)",
		BoxShadow:   "0 0 8px var(--yellow)",
	},
	"failed": {
		BorderColor: "var(--red)",
		BoxShadow:   "0 0 8px var(--red)",
	},
	"dead": {
		BorderColor: "var(--red)",
		BoxShadow:   "0 0 8px var(--red)",
	},
	"merging": {
		BorderColor: "var(--blue)",
		BoxShadow:   "0 0 8px var(--blue)",
	},
	"success": {
		BorderColor: "var(--green)",
		BoxShadow:   "0 0 12px var(--green), 0 0 4px var(--green)",
	},
}

// StatusRingCSS returns inline CSS for the status ring border and glow.
func StatusRingCSS(status string) string {
	style, ok := statusRingStyles[status]
	if !ok {
		style = statusRingStyles["idle"]
	}
	return fmt.Sprintf("border-color: %s; box-shadow: %s;", style.BorderColor, style.BoxShadow)
}
