import { describe, expect, it } from "vitest";
import { type View, viewLabels, views } from "./views";

describe("views", () => {
	it("contains all 7 views", () => {
		expect(views).toHaveLength(7);
		expect(views).toContain("dashboard");
		expect(views).toContain("lesson-editor");
		expect(views).toContain("standards-browser");
		expect(views).toContain("assessment");
		expect(views).toContain("rubric");
		expect(views).toContain("settings");
		expect(views).toContain("setup-wizard");
	});

	it("has a label for every view", () => {
		for (const view of views) {
			expect(viewLabels[view]).toBeTruthy();
		}
	});

	it("View type accepts valid view strings", () => {
		const v: View = "dashboard";
		expect(views).toContain(v);
	});
});
