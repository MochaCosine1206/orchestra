export const views = [
	"dashboard",
	"lesson-editor",
	"standards-browser",
	"assessment",
	"rubric",
	"settings",
	"setup-wizard",
] as const;

export type View = (typeof views)[number];

export const viewLabels: Record<View, string> = {
	dashboard: "Dashboard",
	"lesson-editor": "Lesson Editor",
	"standards-browser": "Standards Browser",
	assessment: "Assessment",
	rubric: "Rubric",
	settings: "Settings",
	"setup-wizard": "Setup Wizard",
};
