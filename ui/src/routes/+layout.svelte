<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';

	let { children } = $props();

	interface NavItem {
		href: string;
		label: string;
		icon: string;
	}

	const navItems: NavItem[] = [
		{
			href: '/devices',
			label: 'Devices',
			icon: 'server'
		},
		{
			href: '/jobs',
			label: 'Jobs',
			icon: 'list'
		},
		{
			href: '/settings',
			label: 'Settings',
			icon: 'cog'
		}
	];

	function isActive(href: string): boolean {
		return page.url.pathname.startsWith(href);
	}
</script>

<div class="flex h-screen bg-gray-950 text-gray-100">
	<!-- Sidebar -->
	<nav
		class="flex w-56 flex-col border-r border-gray-800 bg-gray-900"
		aria-label="Main navigation"
	>
		<!-- Logo / App Name -->
		<div class="flex items-center gap-2 border-b border-gray-800 px-4 py-4">
			<div class="flex h-8 w-8 items-center justify-center rounded bg-blue-600">
				<svg
					xmlns="http://www.w3.org/2000/svg"
					class="h-5 w-5 text-white"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<path d="M12 22c5.523 0 10-4.477 10-10S17.523 2 12 2 2 6.477 2 12s4.477 10 10 10z" />
					<path d="M12 8v4l3 3" />
				</svg>
			</div>
			<span class="text-sm font-semibold tracking-wide text-white">FarmHand</span>
		</div>

		<!-- Nav Items -->
		<ul class="flex flex-col gap-1 p-2" role="list">
			{#each navItems as item}
				<li>
					<a
						href={item.href}
						class="flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors
							{isActive(item.href)
							? 'bg-blue-600 text-white'
							: 'text-gray-400 hover:bg-gray-800 hover:text-gray-100'}"
						aria-current={isActive(item.href) ? 'page' : undefined}
					>
						<!-- Server/Devices icon -->
						{#if item.icon === 'server'}
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
								<rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
								<rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
								<line x1="6" y1="6" x2="6.01" y2="6" />
								<line x1="6" y1="18" x2="6.01" y2="18" />
							</svg>
						{/if}

						<!-- Jobs/List icon -->
						{#if item.icon === 'list'}
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
								<line x1="8" y1="6" x2="21" y2="6" />
								<line x1="8" y1="12" x2="21" y2="12" />
								<line x1="8" y1="18" x2="21" y2="18" />
								<line x1="3" y1="6" x2="3.01" y2="6" />
								<line x1="3" y1="12" x2="3.01" y2="12" />
								<line x1="3" y1="18" x2="3.01" y2="18" />
							</svg>
						{/if}

						<!-- Settings/Cog icon -->
						{#if item.icon === 'cog'}
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
								<circle cx="12" cy="12" r="3" />
								<path
									d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"
								/>
							</svg>
						{/if}

						{item.label}
					</a>
				</li>
			{/each}
		</ul>

		<!-- Version footer -->
		<div class="mt-auto border-t border-gray-800 px-4 py-3">
			<p class="text-xs text-gray-600">v0.1.0</p>
		</div>
	</nav>

	<!-- Main content area -->
	<main class="flex flex-1 flex-col overflow-auto">
		{@render children()}
	</main>
</div>
