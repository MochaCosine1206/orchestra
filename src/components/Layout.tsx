import { useAppStore } from "../appStore";
import Dashboard from "./Dashboard";
import Placeholder from "./Placeholder";
import SetupWizard from "./SetupWizard";
import Sidebar from "./Sidebar";

function ContentArea() {
	const currentView = useAppStore((s) => s.currentView);

	switch (currentView) {
		case "dashboard":
			return <Dashboard />;
		case "setup-wizard":
			return <SetupWizard />;
		default:
			return <Placeholder view={currentView} />;
	}
}

export default function Layout() {
	return (
		<div className="flex h-screen">
			<Sidebar />
			<main className="flex flex-1 flex-col overflow-auto">
				<ContentArea />
			</main>
		</div>
	);
}
