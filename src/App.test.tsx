import { render, screen } from "@testing-library/react";
import { beforeEach, expect, test } from "vitest";
import App from "./App";
import { useAppStore } from "./appStore";

beforeEach(() => {
	useAppStore.setState({ currentView: "setup-wizard" });
});

test("renders setup wizard by default", () => {
	render(<App />);
	expect(screen.getByText("Setup Wizard")).toBeInTheDocument();
});
