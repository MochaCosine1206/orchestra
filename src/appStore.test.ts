import { describe, expect, it } from "vitest";
import { useAppStore } from "./appStore";

describe("appStore", () => {
	it("defaults currentView to setup-wizard", () => {
		const state = useAppStore.getState();
		expect(state.currentView).toBe("setup-wizard");
	});

	it("defaults isGenerating to false", () => {
		const state = useAppStore.getState();
		expect(state.isGenerating).toBe(false);
	});

	it("defaults editorOpen to false", () => {
		const state = useAppStore.getState();
		expect(state.editorOpen).toBe(false);
	});

	it("navigate changes currentView", () => {
		useAppStore.getState().navigate("dashboard");
		expect(useAppStore.getState().currentView).toBe("dashboard");
		useAppStore.getState().navigate("setup-wizard");
	});

	it("setGenerating updates isGenerating", () => {
		useAppStore.getState().setGenerating(true);
		expect(useAppStore.getState().isGenerating).toBe(true);
		useAppStore.getState().setGenerating(false);
	});

	it("setEditorOpen updates editorOpen", () => {
		useAppStore.getState().setEditorOpen(true);
		expect(useAppStore.getState().editorOpen).toBe(true);
		useAppStore.getState().setEditorOpen(false);
	});
});
