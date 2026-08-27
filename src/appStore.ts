import { create } from "zustand";
import type { View } from "./views";

interface AppState {
	currentView: View;
	isGenerating: boolean;
	editorOpen: boolean;
	navigate: (view: View) => void;
	setGenerating: (value: boolean) => void;
	setEditorOpen: (value: boolean) => void;
}

export const useAppStore = create<AppState>((set) => ({
	currentView: "setup-wizard",
	isGenerating: false,
	editorOpen: false,
	navigate: (view) => set({ currentView: view }),
	setGenerating: (value) => set({ isGenerating: value }),
	setEditorOpen: (value) => set({ editorOpen: value }),
}));
