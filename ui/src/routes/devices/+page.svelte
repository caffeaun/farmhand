<script lang="ts">
	import { getDevices, wakeDevice, rebootDevice } from '$lib/api';
	import { FarmhandWS } from '$lib/ws';
	import Toast from '$lib/components/Toast.svelte';
	import type { Device, Toast as ToastType } from '$lib/types';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	type LoadState = 'loading' | 'ready' | 'error';

	let loadState = $state<LoadState>('loading');
	let devices = $state<Device[]>([]);
	let errorMessage = $state('');
	let toasts = $state<ToastType[]>([]);

	// Track which devices currently have in-flight action requests
	let pendingWake = $state(new Set<string>());
	let pendingReboot = $state(new Set<string>());

	// Derived: total device count
	let deviceCount = $derived(devices.length);

	// ---------------------------------------------------------------------------
	// Toast helpers
	// ---------------------------------------------------------------------------

	function addToast(message: string, type: 'success' | 'error') {
		const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
		toasts = [...toasts, { id, message, type }];
		// Auto-dismiss after 3 seconds
		setTimeout(() => dismissToast(id), 3000);
	}

	function dismissToast(id: string) {
		toasts = toasts.filter((t) => t.id !== id);
	}

	// ---------------------------------------------------------------------------
	// Load devices from API
	// ---------------------------------------------------------------------------

	async function loadDevices() {
		loadState = 'loading';
		errorMessage = '';
		try {
			devices = await getDevices();
			loadState = 'ready';
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : 'Failed to load devices.';
			loadState = 'error';
		}
	}

	// ---------------------------------------------------------------------------
	// WebSocket – update device rows in-place on device_update events
	// ---------------------------------------------------------------------------

	$effect(() => {
		loadDevices();

		const ws = new FarmhandWS((msg: { type: string; payload: unknown }) => {
			if (msg.type === 'device_update') {
				const updated = msg.payload as Partial<Device> & { id: string };
				// Update the matching device in-place without a full reload
				devices = devices.map((d) => (d.id === updated.id ? { ...d, ...updated } : d));
			}
		});
		ws.connect();

		return () => ws.close();
	});

	// ---------------------------------------------------------------------------
	// Action handlers
	// ---------------------------------------------------------------------------

	async function handleWake(device: Device) {
		if (pendingWake.has(device.id)) return;
		// Reassign to trigger reactivity (Set mutations are not tracked)
		pendingWake = new Set([...pendingWake, device.id]);
		try {
			await wakeDevice(device.id);
			addToast(`Wake signal sent to ${device.model}.`, 'success');
		} catch (err) {
			addToast(
				`Failed to wake ${device.model}: ${err instanceof Error ? err.message : 'Unknown error'}`,
				'error'
			);
		} finally {
			const next = new Set(pendingWake);
			next.delete(device.id);
			pendingWake = next;
		}
	}

	async function handleReboot(device: Device) {
		if (pendingReboot.has(device.id)) return;
		const confirmed = window.confirm(
			`Reboot ${device.model}?\n\nThis will restart the device immediately.`
		);
		if (!confirmed) return;

		pendingReboot = new Set([...pendingReboot, device.id]);
		try {
			await rebootDevice(device.id);
			addToast(`Reboot initiated for ${device.model}.`, 'success');
		} catch (err) {
			addToast(
				`Failed to reboot ${device.model}: ${err instanceof Error ? err.message : 'Unknown error'}`,
				'error'
			);
		} finally {
			const next = new Set(pendingReboot);
			next.delete(device.id);
			pendingReboot = next;
		}
	}

	// ---------------------------------------------------------------------------
	// Display helpers
	// ---------------------------------------------------------------------------

	const statusBadgeClass: Record<string, string> = {
		online: 'bg-green-500/20 text-green-400 border border-green-500/30',
		offline: 'bg-gray-500/20 text-gray-400 border border-gray-500/30',
		busy: 'bg-yellow-500/20 text-yellow-400 border border-yellow-500/30'
	};

	const statusDotClass: Record<string, string> = {
		online: 'bg-green-400',
		offline: 'bg-gray-500',
		busy: 'bg-yellow-400'
	};

	const statusLabel: Record<string, string> = {
		online: 'Online',
		offline: 'Offline',
		busy: 'Busy'
	};

	function formatLastSeen(iso: string): string {
		const date = new Date(iso);
		if (isNaN(date.getTime())) return iso;
		const diffMs = Date.now() - date.getTime();
		const minutes = Math.floor(diffMs / 60_000);
		if (minutes < 1) return 'Just now';
		if (minutes < 60) return `${minutes}m ago`;
		const hours = Math.floor(minutes / 60);
		if (hours < 24) return `${hours}h ago`;
		return date.toLocaleDateString();
	}
</script>

<svelte:head>
	<title>Devices — FarmHand</title>
</svelte:head>

<!-- Toast notifications -->
<Toast {toasts} ondismiss={dismissToast} />

<div class="p-6">
	<!-- Page header -->
	<div class="mb-6 flex items-center justify-between">
		<div>
			<h1 class="text-xl font-semibold text-white">Devices</h1>
			{#if loadState === 'ready'}
				<p class="mt-1 text-sm text-gray-400">
					{deviceCount}
					{deviceCount === 1 ? 'device' : 'devices'} registered
				</p>
			{/if}
		</div>

		{#if loadState === 'error'}
			<button
				onclick={loadDevices}
				class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500"
			>
				Retry
			</button>
		{/if}
	</div>

	<!-- ── Loading skeleton ────────────────────────────────────────────────── -->
	{#if loadState === 'loading'}
		<div
			role="status"
			aria-label="Loading devices"
			class="overflow-hidden rounded-lg border border-gray-800 bg-gray-900"
		>
			<table class="w-full text-sm" aria-hidden="true">
				<thead>
					<tr class="border-b border-gray-800">
						{#each ['Model', 'Platform', 'OS Version', 'Status', 'Battery', 'Last Seen', 'Actions'] as col}
							<th class="px-4 py-3 text-left font-medium text-gray-400">{col}</th>
						{/each}
					</tr>
				</thead>
				<tbody>
					{#each { length: 4 } as _, i}
						<tr class="border-b border-gray-800 last:border-0">
							{#each { length: 7 } as _, j}
								<td class="px-4 py-3">
									<div
										class="h-4 animate-pulse rounded bg-gray-800"
										style="width: {50 + ((i * 7 + j) % 5) * 10}%"
									></div>
								</td>
							{/each}
						</tr>
					{/each}
				</tbody>
			</table>
			<span class="sr-only">Loading devices, please wait…</span>
		</div>

	<!-- ── Error state ─────────────────────────────────────────────────────── -->
	{:else if loadState === 'error'}
		<div
			role="alert"
			class="flex flex-col items-center justify-center gap-4 rounded-lg border border-red-900/50 bg-red-950/30 p-12 text-center"
		>
			<svg
				xmlns="http://www.w3.org/2000/svg"
				class="h-10 w-10 text-red-400"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="2"
				stroke-linecap="round"
				stroke-linejoin="round"
				aria-hidden="true"
			>
				<circle cx="12" cy="12" r="10" />
				<line x1="12" y1="8" x2="12" y2="12" />
				<line x1="12" y1="16" x2="12.01" y2="16" />
			</svg>
			<div>
				<p class="text-sm font-medium text-red-300">Failed to load devices</p>
				<p class="mt-1 text-xs text-red-400/70">{errorMessage}</p>
			</div>
			<button
				onclick={loadDevices}
				class="rounded-md bg-red-700 px-4 py-2 text-sm font-medium text-white hover:bg-red-600"
			>
				Try again
			</button>
		</div>

	<!-- ── Empty state ─────────────────────────────────────────────────────── -->
	{:else if devices.length === 0}
		<div
			class="flex flex-col items-center justify-center gap-3 rounded-lg border border-gray-800 bg-gray-900 p-12 text-center"
		>
			<svg
				xmlns="http://www.w3.org/2000/svg"
				class="h-10 w-10 text-gray-600"
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
			<p class="text-sm font-medium text-gray-400">No devices registered</p>
			<p class="text-xs text-gray-600">Connect a device to get started.</p>
		</div>

	<!-- ── Device table ────────────────────────────────────────────────────── -->
	{:else}
		<div class="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-gray-800">
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Model</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Platform</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">OS Version</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Status</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Battery</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Last Seen</th>
						<th scope="col" class="px-4 py-3 text-left font-medium text-gray-400">Actions</th>
					</tr>
				</thead>
				<tbody>
					{#each devices as device (device.id)}
						{@const isLowBattery = device.battery_level < 20}
						{@const wakePending = pendingWake.has(device.id)}
						{@const rebootPending = pendingReboot.has(device.id)}
						<tr class="border-b border-gray-800 last:border-0 hover:bg-gray-800/40">

							<!-- Model -->
							<td class="px-4 py-3 font-medium text-white">{device.model}</td>

							<!-- Platform -->
							<td class="px-4 py-3 text-gray-300">{device.platform}</td>

							<!-- OS Version -->
							<td class="px-4 py-3 font-mono text-gray-300">{device.os_version}</td>

							<!-- Status badge -->
							<td class="px-4 py-3">
								<span
									class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium
										{statusBadgeClass[device.status] ?? statusBadgeClass.offline}"
								>
									<span
										class="h-1.5 w-1.5 rounded-full {statusDotClass[device.status] ?? 'bg-gray-500'}"
									></span>
									{statusLabel[device.status] ?? device.status}
								</span>
							</td>

							<!-- Battery -->
							<td class="px-4 py-3">
								<span
									class="inline-flex items-center gap-1 tabular-nums
										{isLowBattery ? 'font-semibold text-red-400' : 'text-gray-300'}"
									title={isLowBattery ? 'Low battery' : undefined}
								>
									{#if isLowBattery}
										<!-- Low battery icon -->
										<svg
											xmlns="http://www.w3.org/2000/svg"
											class="h-4 w-4"
											viewBox="0 0 24 24"
											fill="none"
											stroke="currentColor"
											stroke-width="2"
											stroke-linecap="round"
											stroke-linejoin="round"
											aria-label="Low battery"
										>
											<rect x="2" y="7" width="16" height="10" rx="2" ry="2" />
											<path d="M22 11v2" />
										</svg>
									{/if}
									{device.battery_level}%
								</span>
							</td>

							<!-- Last seen -->
							<td class="px-4 py-3 text-gray-400">
								<time datetime={device.last_seen_at}>{formatLastSeen(device.last_seen_at)}</time>
							</td>

							<!-- Actions -->
							<td class="px-4 py-3">
								<div class="flex items-center gap-2">
									<button
										onclick={() => handleWake(device)}
										disabled={wakePending}
										aria-label="Wake {device.model}"
										class="rounded px-2.5 py-1 text-xs font-medium transition-colors
											disabled:cursor-not-allowed disabled:opacity-40
											bg-gray-700 text-gray-200 hover:bg-gray-600 hover:text-white"
									>
										{wakePending ? 'Waking…' : 'Wake'}
									</button>

									<button
										onclick={() => handleReboot(device)}
										disabled={rebootPending}
										aria-label="Reboot {device.model}"
										class="rounded px-2.5 py-1 text-xs font-medium transition-colors
											disabled:cursor-not-allowed disabled:opacity-40
											bg-red-900/50 text-red-300 hover:bg-red-800/70 hover:text-red-100"
									>
										{rebootPending ? 'Rebooting…' : 'Reboot'}
									</button>
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
