<script lang="ts">
	import type { Toast } from '$lib/types';

	interface Props {
		toasts: Toast[];
		ondismiss: (id: string) => void;
	}

	let { toasts, ondismiss }: Props = $props();
</script>

<!-- Toast container — fixed to top-right -->
<div
	role="region"
	aria-label="Notifications"
	aria-live="polite"
	class="pointer-events-none fixed right-4 top-4 z-50 flex flex-col gap-2"
>
	{#each toasts as toast (toast.id)}
		<div
			role="status"
			class="pointer-events-auto flex items-center justify-between gap-3 rounded-lg px-4 py-3 text-sm font-medium shadow-lg
				{toast.type === 'success'
				? 'bg-green-800 text-green-100'
				: 'bg-red-800 text-red-100'}"
		>
			<span>{toast.message}</span>
			<button
				onclick={() => ondismiss(toast.id)}
				aria-label="Dismiss notification"
				class="shrink-0 opacity-70 hover:opacity-100"
			>
				<svg
					xmlns="http://www.w3.org/2000/svg"
					class="h-4 w-4"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<line x1="18" y1="6" x2="6" y2="18" />
					<line x1="6" y1="6" x2="18" y2="18" />
				</svg>
			</button>
		</div>
	{/each}
</div>
