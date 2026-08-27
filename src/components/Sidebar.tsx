import { useAppStore } from "../appStore";
import { type View, viewLabels, views } from "../views";

const navViews: View[] = views.filter((v) => v !== "setup-wizard");

export default function Sidebar() {
	const currentView = useAppStore((s) => s.currentView);
	const navigate = useAppStore((s) => s.navigate);

	return (
		<nav className="flex w-60 flex-col gap-1 border-r border-gray-200 bg-gray-50 p-4">
			<h2 className="mb-4 text-lg font-semibold text-gray-800">
				Orchestra
			</h2>
			{navViews.map((view) => (
				<button
					key={view}
					type="button"
					onClick={() => navigate(view)}
					className={`rounded-md px-3 py-2 text-left text-sm transition-colors ${
						currentView === view
							? "bg-blue-100 font-medium text-blue-800"
							: "text-gray-600 hover:bg-gray-100 hover:text-gray-900"
					}`}
				>
					{viewLabels[view]}
				</button>
			))}
		</nav>
	);
}
