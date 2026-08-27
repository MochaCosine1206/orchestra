import { type View, viewLabels } from "../views";

interface PlaceholderProps {
	view: View;
}

export default function Placeholder({ view }: PlaceholderProps) {
	return (
		<div className="flex flex-1 items-center justify-center p-8">
			<p className="text-lg text-gray-500">
				Coming soon: {viewLabels[view]}
			</p>
		</div>
	);
}
