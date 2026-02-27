<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { getJobs, createJob, deleteJob } from '$lib/api';
	import { FarmhandWS } from '$lib/ws';
	import type { Job, JobStatus, CreateJobRequest } from '$lib/types';

	// ─── Filter state from URL ────────────────────────────────────────────────

	type FilterTab = 'all' | JobStatus;

	const FILTER_TABS: { value: FilterTab; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'queued', label: 'Queued' },
		{ value: 'running', label: 'Running' },
		{ value: 'completed', label: 'Completed' },
		{ value: 'failed', label: 'Failed' }
	];

	let activeFilter: FilterTab = $derived(
		(page.url.searchParams.get('status') as FilterTab) ?? 'all'
	);

	function setFilter(value: FilterTab) {
		const url = new URL(page.url);
		if (value === 'all') {
			url.searchParams.delete('status');
		} else {
			url.searchParams.set('status', value);
		}
		goto(url, { replaceState: true, noScroll: true });
	}

	// ─── Jobs data ───────────────────────────────────────────────────────────

	type LoadState = 'loading' | 'loaded' | 'error';

	let loadState = $state<LoadState>('loading');
	let jobs = $state<Job[]>([]);
	let errorMessage = $state('');

	function fetchJobs(filter: FilterTab) {
		const jobFilter = filter === 'all' ? undefined : { status: filter };
		getJobs(jobFilter).then(
			(result) => {
				jobs = result;
				loadState = 'loaded';
			},
			(err) => {
				errorMessage = err instanceof Error ? err.message : 'Failed to load jobs';
				loadState = 'error';
			}
		);
	}

	/** Manually re-trigger a load (e.g. after an error). */
	function retryLoad() {
		loadState = 'loading';
		errorMessage = '';
		fetchJobs(activeFilter);
	}

	$effect(() => {
		// Synchronously read activeFilter to register the reactive dependency.
		// Svelte only tracks values read in the synchronous portion of the effect.
		const filterForEffect = activeFilter;

		loadState = 'loading';
		errorMessage = '';

		fetchJobs(filterForEffect);
	});

	// ─── WebSocket: live status updates ──────────────────────────────────────

	$effect(() => {
		const ws = new FarmhandWS((msg) => {
			if (msg.type !== 'job_update') return;
			const updated = msg.payload as Job;
			jobs = jobs.map((j) => (j.id === updated.id ? updated : j));
		});
		ws.connect();

		return () => {
			ws.close();
		};
	});

	// ─── New Job slide-over panel ─────────────────────────────────────────────

	let showPanel = $state(false);
	let panelSubmitting = $state(false);
	let panelError = $state('');

	// Form fields
	let formCommand = $state('');
	let formPlatform = $state<'any' | 'android' | 'ios'>('any');
	let formTags = $state('');
	let formTimeout = $state(30);
	let commandError = $state('');

	function openPanel() {
		formCommand = '';
		formPlatform = 'any';
		formTags = '';
		formTimeout = 30;
		commandError = '';
		panelError = '';
		showPanel = true;
	}

	function closePanel() {
		showPanel = false;
	}

	async function submitNewJob() {
		commandError = '';
		panelError = '';

		if (!formCommand.trim()) {
			commandError = 'Test command is required';
			return;
		}

		panelSubmitting = true;
		try {
			const payload: CreateJobRequest = {
				test_command: formCommand.trim(),
				device_filter: {
					platform: formPlatform !== 'any' ? formPlatform : undefined,
					tags: formTags.trim() ? formTags.trim().split(',').map((t) => t.trim()) : undefined
				},
				timeout_minutes: formTimeout
			};

			const newJob = await createJob(payload);
			jobs = [newJob, ...jobs];
			closePanel();
		} catch (err) {
			panelError = err instanceof Error ? err.message : 'Failed to create job';
		} finally {
			panelSubmitting = false;
		}
	}

	// ─── Delete confirmation ──────────────────────────────────────────────────

	let confirmDeleteId = $state<string | null>(null);
	let deleteError = $state('');

	function promptDelete(id: string) {
		confirmDeleteId = id;
		deleteError = '';
	}

	function cancelDelete() {
		confirmDeleteId = null;
	}

	async function confirmDelete() {
		if (!confirmDeleteId) return;
		const id = confirmDeleteId;
		try {
			await deleteJob(id);
			jobs = jobs.filter((j) => j.id !== id);
			confirmDeleteId = null;
		} catch (err) {
			deleteError = err instanceof Error ? err.message : 'Failed to delete job';
		}
	}

	// ─── Helpers ─────────────────────────────────────────────────────────────

	const STATUS_BADGE: Record<JobStatus, string> = {
		queued: 'bg-blue-500/20 text-blue-400',
		running: 'bg-amber-500/20 text-amber-400',
		completed: 'bg-green-500/20 text-green-400',
		failed: 'bg-red-500/20 text-red-400'
	};

	function truncateId(id: string): string {
		return id.length > 8 ? id.slice(0, 8) : id;
	}

	function truncateCommand(cmd: string, max = 60): string {
		return cmd.length > max ? cmd.slice(0, max) + '…' : cmd;
	}

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleString();
	}

	function calcDuration(job: Job): string {
		const start = job.started_at ? new Date(job.started_at).getTime() : null;
		const end = job.completed_at ? new Date(job.completed_at).getTime() : null;
		if (!start) return '—';
		const ms = (end ?? Date.now()) - start;
		const secs = Math.floor(ms / 1000);
		if (secs < 60) return `${secs}s`;
		const mins = Math.floor(secs / 60);
		const rem = secs % 60;
		return `${mins}m ${rem}s`;
	}
</script>

<svelte:head>
	<title>Jobs — FarmHand</title>
</svelte:head>

<div class="p-6">
	<!-- Header -->
	<div class="mb-6 flex items-center justify-between">
		<div>
			<h1 class="text-xl font-semibold text-white">Jobs</h1>
			{#if loadState === 'loaded'}
				<p class="mt-1 text-sm text-gray-400">{jobs.length} job{jobs.length !== 1 ? 's' : ''}</p>
			{/if}
		</div>
		<button
			onclick={openPanel}
			class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500"
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
				<line x1="12" y1="5" x2="12" y2="19" />
				<line x1="5" y1="12" x2="19" y2="12" />
			</svg>
			New Job
		</button>
	</div>

	<!-- Filter tabs -->
	<div class="mb-4 flex gap-1 rounded-lg bg-gray-800/50 p-1" role="tablist" aria-label="Filter jobs by status">
		{#each FILTER_TABS as tab}
			<button
				role="tab"
				aria-selected={activeFilter === tab.value}
				onclick={() => setFilter(tab.value)}
				class="rounded px-3 py-1.5 text-sm font-medium transition-colors
					{activeFilter === tab.value
						? 'bg-gray-700 text-white'
						: 'text-gray-400 hover:text-gray-200'}"
			>
				{tab.label}
			</button>
		{/each}
	</div>

	<!-- Loading state -->
	{#if loadState === 'loading'}
		<div class="flex items-center justify-center py-20" aria-busy="true" aria-label="Loading jobs">
			<svg
				class="h-6 w-6 animate-spin text-gray-500"
				xmlns="http://www.w3.org/2000/svg"
				fill="none"
				viewBox="0 0 24 24"
				aria-hidden="true"
			>
				<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
				<path
					class="opacity-75"
					fill="currentColor"
					d="M4 12a8 8 0 018-8v8H4z"
				/>
			</svg>
			<span class="ml-3 text-sm text-gray-500">Loading jobs…</span>
		</div>

	<!-- Error state -->
	{:else if loadState === 'error'}
		<div class="rounded-lg border border-red-800 bg-red-900/20 p-6 text-center" role="alert">
			<p class="text-sm text-red-400">{errorMessage}</p>
			<button
				onclick={retryLoad}
				class="mt-3 text-sm text-red-300 underline hover:text-red-200"
			>
				Try again
			</button>
		</div>

	<!-- Empty state -->
	{:else if jobs.length === 0}
		<div class="rounded-lg border border-gray-800 bg-gray-900/50 py-16 text-center">
			<svg
				xmlns="http://www.w3.org/2000/svg"
				class="mx-auto h-10 w-10 text-gray-700"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="1.5"
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
			<p class="mt-3 text-sm text-gray-500">
				{activeFilter === 'all' ? 'No jobs yet.' : `No ${activeFilter} jobs.`}
			</p>
			{#if activeFilter === 'all'}
				<button
					onclick={openPanel}
					class="mt-3 text-sm text-blue-400 underline hover:text-blue-300"
				>
					Create your first job
				</button>
			{/if}
		</div>

	<!-- Jobs table -->
	{:else}
		<div class="overflow-hidden rounded-lg border border-gray-800">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-gray-800 bg-gray-900/80 text-left">
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">ID</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">Status</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">Command</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">Devices</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">Created</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">Duration</th>
						<th class="px-4 py-3 text-xs font-medium tracking-wide text-gray-400">
							<span class="sr-only">Actions</span>
						</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-gray-800">
					{#each jobs as job (job.id)}
						<tr class="bg-gray-900 transition-colors hover:bg-gray-800/60">
							<!-- ID -->
							<td class="px-4 py-3">
								<a
									href="/jobs/{job.id}"
									class="font-mono text-xs text-blue-400 hover:text-blue-300 hover:underline"
								>
									{truncateId(job.id)}
								</a>
							</td>

							<!-- Status badge -->
							<td class="px-4 py-3">
								<span
									class="inline-block rounded px-2 py-0.5 text-xs font-medium capitalize {STATUS_BADGE[job.status]}"
								>
									{job.status}
								</span>
							</td>

							<!-- Command -->
							<td class="px-4 py-3 max-w-xs">
								<span class="font-mono text-xs text-gray-300" title={job.test_command}>
									{truncateCommand(job.test_command)}
								</span>
							</td>

							<!-- Devices count -->
							<td class="px-4 py-3 text-xs text-gray-400">
								{job.results.length}
							</td>

							<!-- Created at -->
							<td class="px-4 py-3 text-xs text-gray-400 whitespace-nowrap">
								{formatDate(job.created_at)}
							</td>

							<!-- Duration -->
							<td class="px-4 py-3 text-xs text-gray-400 tabular-nums">
								{calcDuration(job)}
							</td>

							<!-- Actions: Delete -->
							<td class="px-4 py-3 text-right">
								<button
									onclick={() => promptDelete(job.id)}
									aria-label="Delete job {truncateId(job.id)}"
									class="rounded p-1 text-gray-600 transition-colors hover:bg-red-900/30 hover:text-red-400"
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
										<polyline points="3 6 5 6 21 6" />
										<path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6" />
										<path d="M10 11v6M14 11v6" />
										<path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2" />
									</svg>
								</button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<!-- ─── New Job slide-over panel ─────────────────────────────────────────── -->

{#if showPanel}
	<!-- Backdrop -->
	<div
		class="fixed inset-0 z-40 bg-black/50"
		onclick={closePanel}
		role="presentation"
		aria-hidden="true"
	></div>

	<!-- Panel -->
	<div
		class="fixed inset-y-0 right-0 z-50 flex w-full max-w-md flex-col border-l border-gray-800 bg-gray-950 shadow-2xl"
		aria-label="New job"
		aria-modal="true"
		role="dialog"
	>
		<!-- Panel header -->
		<div class="flex items-center justify-between border-b border-gray-800 px-6 py-4">
			<h2 class="text-base font-semibold text-white">New Job</h2>
			<button
				onclick={closePanel}
				aria-label="Close panel"
				class="rounded p-1 text-gray-500 transition-colors hover:bg-gray-800 hover:text-gray-300"
			>
				<svg
					xmlns="http://www.w3.org/2000/svg"
					class="h-5 w-5"
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

		<!-- Panel form -->
		<div class="flex-1 overflow-y-auto px-6 py-5">
			<form
				onsubmit={(e) => {
					e.preventDefault();
					void submitNewJob();
				}}
				novalidate
				id="new-job-form"
			>
				<!-- Test command -->
				<div class="mb-5">
					<label for="form-command" class="mb-1.5 block text-sm font-medium text-gray-300">
						Test Command <span class="text-red-400" aria-hidden="true">*</span>
					</label>
					<textarea
						id="form-command"
						bind:value={formCommand}
						rows={4}
						placeholder="e.g. pytest tests/ --tb=short"
						required
						aria-required="true"
						aria-describedby={commandError ? 'command-error' : undefined}
						aria-invalid={commandError ? 'true' : undefined}
						class="w-full resize-none rounded-md border bg-gray-900 px-3 py-2 font-mono text-sm text-gray-200 placeholder-gray-600 transition-colors
							{commandError
								? 'border-red-600 focus:border-red-500 focus:ring-red-500/30'
								: 'border-gray-700 focus:border-blue-500 focus:ring-blue-500/30'}
							focus:outline-none focus:ring-2"
					></textarea>
					{#if commandError}
						<p id="command-error" class="mt-1 text-xs text-red-400" role="alert">
							{commandError}
						</p>
					{/if}
				</div>

				<!-- Platform filter -->
				<div class="mb-5">
					<label for="form-platform" class="mb-1.5 block text-sm font-medium text-gray-300">
						Device Platform
					</label>
					<select
						id="form-platform"
						bind:value={formPlatform}
						class="w-full rounded-md border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-200 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30"
					>
						<option value="any">Any</option>
						<option value="android">Android</option>
						<option value="ios">iOS</option>
					</select>
				</div>

				<!-- Tags filter -->
				<div class="mb-5">
					<label for="form-tags" class="mb-1.5 block text-sm font-medium text-gray-300">
						Device Tags
					</label>
					<input
						id="form-tags"
						type="text"
						bind:value={formTags}
						placeholder="e.g. production, emea"
						class="w-full rounded-md border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-200 placeholder-gray-600 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30"
					/>
					<p class="mt-1 text-xs text-gray-500">Comma-separated list of tags to filter devices.</p>
				</div>

				<!-- Timeout -->
				<div class="mb-5">
					<label for="form-timeout" class="mb-1.5 block text-sm font-medium text-gray-300">
						Timeout (minutes)
					</label>
					<input
						id="form-timeout"
						type="number"
						bind:value={formTimeout}
						min={1}
						max={1440}
						class="w-full rounded-md border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-200 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30"
					/>
				</div>

				{#if panelError}
					<div class="rounded-md border border-red-800 bg-red-900/20 px-4 py-3" role="alert">
						<p class="text-sm text-red-400">{panelError}</p>
					</div>
				{/if}
			</form>
		</div>

		<!-- Panel footer -->
		<div class="flex items-center justify-end gap-3 border-t border-gray-800 px-6 py-4">
			<button
				type="button"
				onclick={closePanel}
				disabled={panelSubmitting}
				class="rounded-md px-4 py-2 text-sm font-medium text-gray-400 transition-colors hover:bg-gray-800 hover:text-gray-200 disabled:opacity-50"
			>
				Cancel
			</button>
			<button
				type="submit"
				form="new-job-form"
				disabled={panelSubmitting}
				class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-60"
			>
				{#if panelSubmitting}
					<svg
						class="h-4 w-4 animate-spin"
						xmlns="http://www.w3.org/2000/svg"
						fill="none"
						viewBox="0 0 24 24"
						aria-hidden="true"
					>
						<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
						<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
					</svg>
					Creating…
				{:else}
					Create Job
				{/if}
			</button>
		</div>
	</div>
{/if}

<!-- ─── Delete confirmation dialog ──────────────────────────────────────── -->

{#if confirmDeleteId}
	<!-- Backdrop -->
	<div
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
		role="presentation"
	>
		<dialog
			open
			class="w-full max-w-sm rounded-xl border border-gray-800 bg-gray-900 p-6 shadow-2xl"
			aria-labelledby="delete-dialog-title"
			aria-modal="true"
		>
			<h3 id="delete-dialog-title" class="text-base font-semibold text-white">Delete Job</h3>
			<p class="mt-2 text-sm text-gray-400">
				Are you sure you want to delete job
				<span class="font-mono text-gray-200">{truncateId(confirmDeleteId)}</span>? This action
				cannot be undone.
			</p>

			{#if deleteError}
				<p class="mt-3 text-sm text-red-400" role="alert">{deleteError}</p>
			{/if}

			<div class="mt-5 flex justify-end gap-3">
				<button
					onclick={cancelDelete}
					class="rounded-md px-4 py-2 text-sm font-medium text-gray-400 transition-colors hover:bg-gray-800 hover:text-gray-200"
				>
					Cancel
				</button>
				<button
					onclick={confirmDelete}
					class="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-500"
				>
					Delete
				</button>
			</div>
		</dialog>
	</div>
{/if}
