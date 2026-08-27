import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { useAppStore } from "../appStore";
import Layout from "./Layout";

describe("Layout", () => {
	beforeEach(() => {
		useAppStore.setState({ currentView: "setup-wizard" });
	});

	it("renders the setup wizard by default", () => {
		render(<Layout />);
		expect(screen.getByText("Setup Wizard")).toBeInTheDocument();
		expect(
			screen.getByText("Welcome — let's configure your workspace."),
		).toBeInTheDocument();
	});

	it("renders Dashboard when navigated", async () => {
		useAppStore.setState({ currentView: "dashboard" });
		render(<Layout />);
		expect(
			screen.getByRole("heading", { name: "Dashboard" }),
		).toBeInTheDocument();
	});

	it("renders Placeholder for views without dedicated components", () => {
		useAppStore.setState({ currentView: "rubric" });
		render(<Layout />);
		expect(screen.getByText("Coming soon: Rubric")).toBeInTheDocument();
	});

	it("sidebar has nav items for all non-wizard views", () => {
		render(<Layout />);
		expect(screen.getByText("Dashboard")).toBeInTheDocument();
		expect(screen.getByText("Lesson Editor")).toBeInTheDocument();
		expect(screen.getByText("Standards Browser")).toBeInTheDocument();
		expect(screen.getByText("Assessment")).toBeInTheDocument();
		expect(screen.getByText("Rubric")).toBeInTheDocument();
		expect(screen.getByText("Settings")).toBeInTheDocument();
	});

	it("clicking a sidebar item navigates to that view", async () => {
		render(<Layout />);
		const user = userEvent.setup();
		await user.click(screen.getByText("Dashboard"));
		expect(useAppStore.getState().currentView).toBe("dashboard");
	});
});
