<script lang="ts">
	import { browser } from '$app/environment';
	import { TOKEN_KEY, getHealth, getConfig, getStats } from '$lib/api';
	import type { ConfigResponse, HealthResponse, StatsResponse } from '$lib/types';

	// ─── Authentication state ─────────────────────────────────────────────────

	/** The value currently shown in the input field. */
	let tokenInput = $state('');
	/** Whether to render the token as plain text (show) or dots (hide). */
	let showToken = $state(false);
	/** Briefly true after a successful save to show the success banner. */
	let tokenSaved = $state(false);
	/** True when localStorage contains a non-empty token. */
	let hasStoredToken = $state(false);

	// Read the current token from localStorage once the component mounts.
	// This runs exactly once (no reactive dependencies inside except `browser`).
	$effect(() => {
		if (!browser) return;
		const stored = localStorage.getItem(TOKEN_KEY) ?? '';
		tokenInput = stored;
		hasStoredToken = stored.length > 0;
		// Eagerly load server data when a token is already present.
		if (stored.length > 0) {
			loadConfig();
			loadStats();
		}
	});

	function handleSaveToken() {
		if (!browser) return;
		tokenInput = tokenInput.trim();
		localStorage.setItem(TOKEN_KEY, tokenInput);
		hasStoredToken = tokenInput.length > 0;
		tokenSaved = true;

		// Reload config and stats immediately using the newly saved token.
		if (tokenInput.length > 0) {
			loadConfig();
			loadStats();
		} else {
			// Token was cleared via the save button (empty input).
			configData = null;
			statsData = null;
			configError = null;
			statsError = null;
		}

		setTimeout(() => {
			tokenSaved = false;
		}, 2500);
	}

	function handleClearToken() {
		if (!browser) return;
		localStorage.removeItem(TOKEN_KEY);
		tokenInput = '';
		hasStoredToken = false;
		configData = null;
		statsData = null;
		configError = null;
		statsError = null;
		connectionStatus = 'idle';
	}

	// ─── Connection test state ────────────────────────────────────────────────

	type ConnectionStatus = 'idle' | 'loading' | 'ok' | 'error';
	let connectionStatus = $state<ConnectionStatus>('idle');
	let connectionDetail = $state('');

	async function testConnection() {
		connectionStatus = 'loading';
		connectionDetail = '';
		try {
			const result: HealthResponse = await getHealth();
			connectionStatus = 'ok';
			connectionDetail = `v${result.version}, uptime ${result.uptime_seconds}s`;
		} catch (err) {
			connectionStatus = 'error';
			connectionDetail = err instanceof Error ? err.message : 'Connection failed';
		}
	}

	// ─── Server config state ──────────────────────────────────────────────────

	let configData = $state<ConfigResponse | null>(null);
	let configLoading = $state(false);
	let configError = $state<string | null>(null);

	async function loadConfig() {
		// getConfig() auto-reads the token from localStorage via buildHeaders().
		configLoading = true;
		configError = null;
		try {
			configData = await getConfig();
		} catch (err) {
			configError = err instanceof Error ? err.message : 'Failed to load config';
			configData = null;
		} finally {
			configLoading = false;
		}
	}

	// ─── Stats state ──────────────────────────────────────────────────────────

	let statsData = $state<StatsResponse | null>(null);
	let statsLoading = $state(false);
	let statsError = $state<string | null>(null);

	async function loadStats() {
		// getStats() auto-reads the token from localStorage via buildHeaders().
		statsLoading = true;
		statsError = null;
		try {
			statsData = await getStats();
		} catch (err) {
			statsError = err instanceof Error ? err.message : 'Failed to load stats';
			statsData = null;
		} finally {
			statsLoading = false;
		}
	}

	// ─── Config key/value display helper ─────────────────────────────────────

	interface KVPair {
		label: string;
		value: string | number;
	}

	function configToKVPairs(cfg: ConfigResponse): KVPair[] {
		return [
			{ label: 'Server host', value: cfg.server.host },
			{ label: 'Server port', value: cfg.server.port },
			{ label: 'Dev mode', value: cfg.server.dev_mode ? 'enabled' : 'disabled' },
			{ label: 'Auth token', value: cfg.server.auth_token || '(none set)' },
			{ label: 'CORS origins', value: cfg.server.cors_origins.join(', ') || '*' },
			{ label: 'Database path', value: cfg.database.path },
			{ label: 'Data retention', value: `${cfg.database.retention_days} days` },
			{ label: 'Auto-discover devices', value: cfg.devices.auto_discover ? 'yes' : 'no' },
			{ label: 'Device poll interval', value: `${cfg.devices.poll_interval_seconds}s` },
			{ label: 'Min battery level', value: `${cfg.devices.min_battery_percent}%` },
			{ label: 'ADB path', value: cfg.devices.adb_path },
			{ label: 'Cleanup between runs', value: cfg.devices.cleanup_between_runs ? 'yes' : 'no' },
			{ label: 'Wake before test', value: cfg.devices.wake_before_test ? 'yes' : 'no' },
			{ label: 'Default job timeout', value: `${cfg.jobs.default_timeout_minutes} min` },
			{ label: 'Max concurrent jobs', value: cfg.jobs.max_concurrent_jobs },
			{ label: 'Artifact dir', value: cfg.jobs.artifact_storage_path },
			{ label: 'Log dir', value: cfg.jobs.log_dir },
			{ label: 'Max artifact size', value: `${cfg.jobs.max_artifact_size_mb} MB` },
			{ label: 'Webhook URL', value: cfg.notifications.webhook_url || '(none)' },
			{ label: 'Notify on', value: cfg.notifications.notify_on.join(', ') || '(none)' }
		];
	}
</script>

<svelte:head>
	<title>Settings — FarmHand</title>
</svelte:head>

<div class="mx-auto max-w-2xl p-6">
	<div class="mb-6">
		<h1 class="text-xl font-semibold text-white">Settings</h1>
		<p class="mt-1 text-sm text-gray-400">Manage your FarmHand token and view server information</p>
	</div>

	<div class="flex flex-col gap-5">
		<!-- ─── Authentication ──────────────────────────────────────────────── -->
		<section aria-labelledby="auth-heading" class="rounded-lg border border-gray-800 bg-gray-900">
			<div class="border-b border-gray-800 px-4 py-3">
				<h2 id="auth-heading" class="text-sm font-medium text-gray-300">Authentication</h2>
			</div>
			<div class="space-y-4 p-4">
				{#if !hasStoredToken}
					<p
						class="rounded border border-yellow-800 bg-yellow-900/30 px-3 py-2 text-sm text-yellow-300"
					>
						No token is saved. Enter your FarmHand bearer token below to authenticate API requests.
						The token is stored only in your browser's localStorage and is never sent to any third
						party.
					</p>
				{/if}

				<div>
					<label for="bearer-token" class="mb-1.5 block text-sm text-gray-400">
						Bearer token
					</label>
					<div class="flex gap-2">
						<div class="relative flex-1">
							<input
								id="bearer-token"
								type={showToken ? 'text' : 'password'}
								bind:value={tokenInput}
								class="w-full rounded border border-gray-700 bg-gray-800 px-3 py-2 pr-10 text-sm text-gray-100 placeholder-gray-600 outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
								placeholder="Enter your bearer token"
								autocomplete="off"
							/>
							<button
								type="button"
								onclick={() => (showToken = !showToken)}
								class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300"
								aria-label={showToken ? 'Hide token' : 'Show token'}
							>
								{#if showToken}
									<!-- Eye-off icon -->
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
										<path
											d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"
										/>
										<path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
										<line x1="1" y1="1" x2="23" y2="23" />
									</svg>
								{:else}
									<!-- Eye icon -->
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
										<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
										<circle cx="12" cy="12" r="3" />
									</svg>
								{/if}
							</button>
						</div>

						<button
							type="button"
							onclick={handleSaveToken}
							class="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-gray-900"
						>
							Save
						</button>

						{#if hasStoredToken}
							<button
								type="button"
								onclick={handleClearToken}
								class="rounded border border-gray-700 px-4 py-2 text-sm font-medium text-gray-400 transition-colors hover:border-red-700 hover:text-red-400 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2 focus:ring-offset-gray-900"
							>
								Clear
							</button>
						{/if}
					</div>
				</div>

				{#if tokenSaved}
					<p role="status" class="text-sm text-green-400">Token saved successfully.</p>
				{/if}
			</div>
		</section>

		<!-- ─── Connection test ────────────────────────────────────────────────── -->
		<section
			aria-labelledby="connection-heading"
			class="rounded-lg border border-gray-800 bg-gray-900"
		>
			<div class="border-b border-gray-800 px-4 py-3">
				<h2 id="connection-heading" class="text-sm font-medium text-gray-300">Connection</h2>
			</div>
			<div class="flex items-center gap-4 p-4">
				<button
					type="button"
					onclick={testConnection}
					disabled={connectionStatus === 'loading'}
					class="rounded border border-gray-700 px-4 py-2 text-sm font-medium text-gray-300 transition-colors hover:border-gray-600 hover:text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-gray-900 disabled:cursor-not-allowed disabled:opacity-50"
				>
					Test Connection
				</button>

				{#if connectionStatus === 'loading'}
					<span class="flex items-center gap-2 text-sm text-gray-400" role="status">
						<svg
							class="h-4 w-4 animate-spin text-blue-400"
							xmlns="http://www.w3.org/2000/svg"
							fill="none"
							viewBox="0 0 24 24"
							aria-hidden="true"
						>
							<circle
								class="opacity-25"
								cx="12"
								cy="12"
								r="10"
								stroke="currentColor"
								stroke-width="4"
							></circle>
							<path
								class="opacity-75"
								fill="currentColor"
								d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
							></path>
						</svg>
						Testing…
					</span>
				{:else if connectionStatus === 'ok'}
					<span class="flex items-center gap-2 text-sm text-green-400" role="status">
						<svg
							xmlns="http://www.w3.org/2000/svg"
							class="h-4 w-4"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2.5"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
						>
							<polyline points="20 6 9 17 4 12" />
						</svg>
						Connected — {connectionDetail}
					</span>
				{:else if connectionStatus === 'error'}
					<span class="flex items-center gap-2 text-sm text-red-400" role="alert">
						<svg
							xmlns="http://www.w3.org/2000/svg"
							class="h-4 w-4"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							stroke-width="2.5"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
						>
							<line x1="18" y1="6" x2="6" y2="18" />
							<line x1="6" y1="6" x2="18" y2="18" />
						</svg>
						{connectionDetail}
					</span>
				{/if}
			</div>
		</section>

		<!-- ─── Server config ──────────────────────────────────────────────────── -->
		<section
			aria-labelledby="server-config-heading"
			class="rounded-lg border border-gray-800 bg-gray-900"
		>
			<div class="flex items-center justify-between border-b border-gray-800 px-4 py-3">
				<h2 id="server-config-heading" class="text-sm font-medium text-gray-300">Server Config</h2>
				{#if hasStoredToken}
					<button
						type="button"
						onclick={loadConfig}
						disabled={configLoading}
						class="text-xs text-gray-500 hover:text-gray-300 disabled:cursor-not-allowed disabled:opacity-50"
					>
						Refresh
					</button>
				{/if}
			</div>

			<div class="p-4">
				{#if !hasStoredToken}
					<p class="text-sm text-gray-500">
						Set a bearer token above to load the server configuration.
					</p>
				{:else if configLoading}
					<p class="text-sm text-gray-400" role="status">Loading configuration…</p>
				{:else if configError}
					<p class="text-sm text-red-400" role="alert">{configError}</p>
				{:else if configData}
					<dl class="divide-y divide-gray-800">
						{#each configToKVPairs(configData) as pair (pair.label)}
							<div class="flex items-baseline gap-4 py-2">
								<dt class="w-44 shrink-0 text-xs text-gray-500">{pair.label}</dt>
								<dd class="min-w-0 break-all font-mono text-sm text-gray-200">{pair.value}</dd>
							</div>
						{/each}
					</dl>
				{:else}
					<p class="text-sm text-gray-500">No configuration data available.</p>
				{/if}
			</div>
		</section>

		<!-- ─── Stats ──────────────────────────────────────────────────────────── -->
		<section
			aria-labelledby="stats-heading"
			class="rounded-lg border border-gray-800 bg-gray-900"
		>
			<div class="flex items-center justify-between border-b border-gray-800 px-4 py-3">
				<h2 id="stats-heading" class="text-sm font-medium text-gray-300">Live Stats</h2>
				{#if hasStoredToken}
					<button
						type="button"
						onclick={loadStats}
						disabled={statsLoading}
						class="text-xs text-gray-500 hover:text-gray-300 disabled:cursor-not-allowed disabled:opacity-50"
					>
						Refresh
					</button>
				{/if}
			</div>

			<div class="p-4">
				{#if !hasStoredToken}
					<p class="text-sm text-gray-500">Set a bearer token above to load live stats.</p>
				{:else if statsLoading}
					<p class="text-sm text-gray-400" role="status">Loading stats…</p>
				{:else if statsError}
					<p class="text-sm text-red-400" role="alert">{statsError}</p>
				{:else if statsData}
					<div class="grid grid-cols-2 gap-6">
						<!-- Devices column -->
						<div>
							<h3 class="mb-3 text-xs font-medium uppercase tracking-wider text-gray-500">
								Devices
							</h3>
							<dl class="space-y-2">
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Total</dt>
									<dd class="font-mono text-sm font-medium text-white">
										{statsData.devices.total}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Online</dt>
									<dd class="font-mono text-sm font-medium text-green-400">
										{statsData.devices.online}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Offline</dt>
									<dd class="font-mono text-sm font-medium text-gray-400">
										{statsData.devices.offline}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Busy</dt>
									<dd class="font-mono text-sm font-medium text-yellow-400">
										{statsData.devices.busy}
									</dd>
								</div>
							</dl>
						</div>

						<!-- Jobs column -->
						<div>
							<h3 class="mb-3 text-xs font-medium uppercase tracking-wider text-gray-500">Jobs</h3>
							<dl class="space-y-2">
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Total</dt>
									<dd class="font-mono text-sm font-medium text-white">{statsData.jobs.total}</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Queued</dt>
									<dd class="font-mono text-sm font-medium text-yellow-400">
										{statsData.jobs.queued}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Running</dt>
									<dd class="font-mono text-sm font-medium text-blue-400">
										{statsData.jobs.running}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Completed</dt>
									<dd class="font-mono text-sm font-medium text-green-400">
										{statsData.jobs.completed}
									</dd>
								</div>
								<div class="flex items-center justify-between">
									<dt class="text-sm text-gray-400">Failed</dt>
									<dd class="font-mono text-sm font-medium text-red-400">
										{statsData.jobs.failed}
									</dd>
								</div>
							</dl>
						</div>
					</div>
				{:else}
					<p class="text-sm text-gray-500">No stats data available.</p>
				{/if}
			</div>
		</section>
	</div>
</div>
