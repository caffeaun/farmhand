<script lang="ts">
	import { page } from '$app/state';
	import { getJob, getJobArtifacts, TOKEN_KEY } from '$lib/api';
	import type { Artifact, Job, JobResultStatus, JobStatus } from '$lib/types';

	// ─── Route param ─────────────────────────────────────────────────────────────
	const jobId = $derived(page.params.id ?? '');

	// ─── Job state ───────────────────────────────────────────────────────────────
	let job = $state<Job | null>(null);
	let jobLoading = $state(true);
	let jobError = $state<string | null>(null);

	// ─── Artifacts state ─────────────────────────────────────────────────────────
	let artifacts = $state<Artifact[]>([]);
	let artifactsLoading = $state(true);
	let artifactsError = $state<string | null>(null);

	// ─── Log viewer state ────────────────────────────────────────────────────────
	let logLines = $state<string[]>([]);
	let logComplete = $state(false);
	let logError = $state<string | null>(null);
	let sseConnected = $state(false);

	// DOM ref for the log viewport
	let logViewport = $state<HTMLDivElement | null>(null);
	// Whether user is at (or near) the bottom
	let isAtBottom = $state(true);

	// ─── Load job data ────────────────────────────────────────────────────────────
	async function loadJob(id: string) {
		jobLoading = true;
		jobError = null;
		try {
			job = await getJob(id);
		} catch (err) {
			jobError = err instanceof Error ? err.message : 'Failed to load job';
		} finally {
			jobLoading = false;
		}
	}

	// ─── Load artifacts ───────────────────────────────────────────────────────────
	async function loadArtifacts(id: string) {
		artifactsLoading = true;
		artifactsError = null;
		try {
			artifacts = await getJobArtifacts(id);
		} catch (err) {
			artifactsError = err instanceof Error ? err.message : 'Failed to load artifacts';
		} finally {
			artifactsLoading = false;
		}
	}

	// ─── SSE log streaming ───────────────────────────────────────────────────────

	// Track the active EventSource so we can close it on retry or unmount
	let activeEventSource: EventSource | null = null;

	function connectSSE(id: string) {
		// Close any existing connection before creating a new one
		activeEventSource?.close();

		logLines = [];
		logComplete = false;
		logError = null;
		sseConnected = false;

		const token = typeof localStorage !== 'undefined' ? localStorage.getItem(TOKEN_KEY) : null;
		const qs = token ? `?token=${encodeURIComponent(token)}` : '';
		const es = new EventSource(`/api/v1/jobs/${id}/logs${qs}`);
		activeEventSource = es;

		es.onopen = () => {
			sseConnected = true;
		};

		// Default message events carry a log line
		es.onmessage = (event: MessageEvent<string>) => {
			logLines.push(event.data);
		};

		// Named "done" event signals end of log stream
		es.addEventListener('done', () => {
			logComplete = true;
			es.close();
			if (activeEventSource === es) activeEventSource = null;
		});

		es.onerror = () => {
			// Only update state if this is still the active connection
			if (activeEventSource !== es) return;
			sseConnected = false;
			logError = 'Log stream disconnected. Check connection or retry.';
			es.close();
			activeEventSource = null;
		};
	}

	// ─── Reactive effect: fetch data when jobId changes ──────────────────────────
	$effect(() => {
		const id = jobId;
		loadJob(id);
		loadArtifacts(id);
		connectSSE(id);
		// Cleanup: close the EventSource when the effect re-runs or component unmounts
		return () => {
			activeEventSource?.close();
			activeEventSource = null;
		};
	});

	// ─── Auto-scroll log viewer to bottom ────────────────────────────────────────
	$effect(() => {
		// Access logLines.length so this effect re-runs when new lines arrive
		const _len = logLines.length;
		if (isAtBottom && logViewport) {
			logViewport.scrollTop = logViewport.scrollHeight;
		}
	});

	function handleLogScroll() {
		if (!logViewport) return;
		const { scrollTop, scrollHeight, clientHeight } = logViewport;
		// Consider "at bottom" when within 40px of bottom
		isAtBottom = scrollHeight - scrollTop - clientHeight < 40;
	}

	function scrollToBottom() {
		if (!logViewport) return;
		logViewport.scrollTop = logViewport.scrollHeight;
		isAtBottom = true;
	}

	function retrySSE() {
		// Re-connect SSE; connectSSE closes the previous connection internally
		connectSSE(jobId);
	}

	// ─── Utility: status badge styles ────────────────────────────────────────────
	const statusStyles: Record<JobStatus, string> = {
		queued: 'bg-yellow-500/20 text-yellow-400',
		running: 'bg-blue-500/20 text-blue-400',
		completed: 'bg-green-500/20 text-green-400',
		failed: 'bg-red-500/20 text-red-400'
	};

	const resultStatusStyles: Record<JobResultStatus, string> = {
		running: 'bg-blue-500/20 text-blue-400',
		passed: 'bg-green-500/20 text-green-400',
		failed: 'bg-red-500/20 text-red-400',
		error: 'bg-orange-500/20 text-orange-400'
	};

	// ─── Utility: duration from two ISO timestamps ────────────────────────────────
	function formatDuration(startIso: string | null, endIso: string | null): string {
		if (!startIso || !endIso) return '-';
		const ms = new Date(endIso).getTime() - new Date(startIso).getTime();
		if (ms < 0) return '-';
		const totalSeconds = Math.floor(ms / 1000);
		const h = Math.floor(totalSeconds / 3600);
		const m = Math.floor((totalSeconds % 3600) / 60);
		const s = totalSeconds % 60;
		if (h > 0) return `${h}h ${m}m ${s}s`;
		if (m > 0) return `${m}m ${s}s`;
		return `${s}s`;
	}

	// ─── Utility: human-readable file size ───────────────────────────────────────
	function formatBytes(bytes: number): string {
		if (bytes === 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		const value = bytes / Math.pow(1024, i);
		return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
	}

	// ─── Utility: format ISO timestamp ───────────────────────────────────────────
	function formatDate(iso: string | null): string {
		if (!iso) return '-';
		return new Date(iso).toLocaleString();
	}
</script>

<svelte:head>
	<title>Job {jobId} — FarmHand</title>
</svelte:head>

<div class="p-6">
	<!-- Breadcrumb -->
	<nav class="mb-4 flex items-center gap-2 text-sm text-gray-500" aria-label="Breadcrumb">
		<a href="/jobs" class="hover:text-gray-300">Jobs</a>
		<span aria-hidden="true">/</span>
		<span class="font-mono text-gray-300">{jobId}</span>
	</nav>

	<!-- ─── Job metadata section ──────────────────────────────────────────────── -->
	{#if jobLoading}
		<div class="mb-6 rounded-lg border border-gray-800 bg-gray-900 p-4">
			<div class="flex animate-pulse flex-col gap-2">
				<div class="h-4 w-1/3 rounded bg-gray-800"></div>
				<div class="h-3 w-1/4 rounded bg-gray-800"></div>
			</div>
		</div>
	{:else if jobError}
		<div
			class="mb-6 rounded-lg border border-red-800 bg-red-900/20 p-4"
			role="alert"
			aria-live="polite"
		>
			<p class="text-sm text-red-400">{jobError}</p>
			<button
				onclick={() => loadJob(jobId)}
				class="mt-2 rounded bg-red-600 px-3 py-1 text-xs font-medium text-white hover:bg-red-500"
			>
				Retry
			</button>
		</div>
	{:else if job}
		<div class="mb-6 rounded-lg border border-gray-800 bg-gray-900 p-4">
			<div class="flex items-start gap-3">
				<div class="min-w-0 flex-1">
					<p class="mb-1 text-xs text-gray-500">Command</p>
					<p class="font-mono text-sm text-gray-200">{job.test_command}</p>
				</div>
				<span
					class="shrink-0 rounded px-2 py-1 text-xs font-medium capitalize {statusStyles[job.status]}"
				>
					{job.status}
				</span>
			</div>

			<dl class="mt-4 grid grid-cols-2 gap-x-6 gap-y-2 text-xs sm:grid-cols-4">
				<div>
					<dt class="text-gray-500">Job ID</dt>
					<dd class="mt-0.5 font-mono text-gray-300">{job.id}</dd>
				</div>
				<div>
					<dt class="text-gray-500">Created</dt>
					<dd class="mt-0.5 text-gray-300">{formatDate(job.created_at)}</dd>
				</div>
				<div>
					<dt class="text-gray-500">Started</dt>
					<dd class="mt-0.5 text-gray-300">{formatDate(job.started_at)}</dd>
				</div>
				<div>
					<dt class="text-gray-500">Completed</dt>
					<dd class="mt-0.5 text-gray-300">{formatDate(job.completed_at)}</dd>
				</div>
				{#if job.started_at && job.completed_at}
					<div>
						<dt class="text-gray-500">Duration</dt>
						<dd class="mt-0.5 text-gray-300">{formatDuration(job.started_at, job.completed_at)}</dd>
					</div>
				{/if}
				<div>
					<dt class="text-gray-500">Timeout</dt>
					<dd class="mt-0.5 text-gray-300">{job.timeout_minutes}m</dd>
				</div>
			</dl>
		</div>
	{/if}

	<!-- ─── Per-device results ────────────────────────────────────────────────── -->
	<section aria-labelledby="results-heading" class="mb-8">
		<h2 id="results-heading" class="mb-3 text-sm font-medium text-gray-400">
			Results by device
		</h2>

		{#if jobLoading}
			<div class="flex flex-col gap-3">
				{#each [1, 2] as _}
					<div class="h-20 animate-pulse rounded-lg border border-gray-800 bg-gray-900"></div>
				{/each}
			</div>
		{:else if jobError}
			<p class="text-sm text-gray-500">Unable to load results.</p>
		{:else if job && job.results.length === 0}
			<p class="text-sm text-gray-500">No device results yet.</p>
		{:else if job}
			<div class="flex flex-col gap-3">
				{#each job.results as result (result.id)}
					<div class="rounded-lg border border-gray-800 bg-gray-900">
						<!-- Card header -->
						<div class="flex items-center justify-between border-b border-gray-800 px-4 py-3">
							<div class="flex items-center gap-3">
								<span class="font-mono text-sm font-medium text-white">{result.device_id}</span>
								{#if result.started_at && result.completed_at}
									<span class="text-xs text-gray-500">
										{formatDuration(result.started_at, result.completed_at)}
									</span>
								{/if}
							</div>
							<div class="flex items-center gap-2">
								{#if result.exit_code !== null}
									<span class="font-mono text-xs text-gray-500">
										exit {result.exit_code}
									</span>
								{/if}
								<span
									class="rounded px-2 py-0.5 text-xs font-medium capitalize {resultStatusStyles[result.status]}"
								>
									{result.status}
								</span>
							</div>
						</div>

						<!-- Error message if present -->
						{#if result.error_message}
							<div class="border-t border-gray-800 p-4">
								<p class="mb-1 text-xs text-red-400">Error</p>
								<p class="text-sm text-red-300">{result.error_message}</p>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</section>

	<!-- ─── Log viewer (SSE streaming) ───────────────────────────────────────── -->
	<section aria-labelledby="logs-heading" class="mb-8">
		<div class="mb-3 flex items-center justify-between">
			<h2 id="logs-heading" class="text-sm font-medium text-gray-400">Live logs</h2>
			<div class="flex items-center gap-2">
				{#if sseConnected && !logComplete}
					<span class="flex items-center gap-1.5 text-xs text-blue-400">
						<span
							class="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-blue-400"
							aria-hidden="true"
						></span>
						Streaming
					</span>
				{/if}
				{#if logComplete}
					<span class="text-xs text-green-400">Log complete</span>
				{/if}
			</div>
		</div>

		<div class="relative">
			<!-- Log container -->
			<div
				bind:this={logViewport}
				onscroll={handleLogScroll}
				class="h-80 overflow-y-auto rounded-lg border border-gray-800 bg-gray-950 p-3"
				role="log"
				aria-label="Job log output"
				aria-live="polite"
				aria-relevant="additions"
			>
				{#if logLines.length === 0 && !logError && !logComplete}
					<p class="font-mono text-xs text-gray-600">Waiting for log output...</p>
				{:else}
					{#each logLines as line, i (i)}
						<p class="font-mono text-xs leading-5 text-gray-300 whitespace-pre-wrap">{line}</p>
					{/each}
					{#if logComplete}
						<p class="mt-2 font-mono text-xs text-green-500">--- Log complete ---</p>
					{/if}
				{/if}
			</div>

			<!-- Error state with Retry button -->
			{#if logError}
				<div
					class="mt-2 flex items-center justify-between rounded border border-red-800 bg-red-900/20 px-3 py-2"
					role="alert"
				>
					<p class="text-xs text-red-400">{logError}</p>
					<button
						onclick={retrySSE}
						class="ml-3 shrink-0 rounded bg-red-600 px-3 py-1 text-xs font-medium text-white hover:bg-red-500"
					>
						Retry
					</button>
				</div>
			{/if}

			<!-- Scroll to bottom floating button -->
			{#if !isAtBottom}
				<button
					onclick={scrollToBottom}
					class="absolute bottom-4 right-4 flex items-center gap-1 rounded-full bg-blue-600 px-3 py-1.5 text-xs font-medium text-white shadow-lg hover:bg-blue-500"
					aria-label="Scroll to bottom of log"
				>
					<svg
						xmlns="http://www.w3.org/2000/svg"
						class="h-3.5 w-3.5"
						viewBox="0 0 24 24"
						fill="none"
						stroke="currentColor"
						stroke-width="2.5"
						stroke-linecap="round"
						stroke-linejoin="round"
						aria-hidden="true"
					>
						<polyline points="6 9 12 15 18 9" />
					</svg>
					Scroll to bottom
				</button>
			{/if}
		</div>
	</section>

	<!-- ─── Artifacts section ─────────────────────────────────────────────────── -->
	<section aria-labelledby="artifacts-heading">
		<h2 id="artifacts-heading" class="mb-3 text-sm font-medium text-gray-400">Artifacts</h2>

		{#if artifactsLoading}
			<div class="h-12 animate-pulse rounded-lg border border-gray-800 bg-gray-900"></div>
		{:else if artifactsError}
			<div
				class="rounded border border-red-800 bg-red-900/20 px-3 py-2"
				role="alert"
				aria-live="polite"
			>
				<p class="text-xs text-red-400">{artifactsError}</p>
				<button
					onclick={() => loadArtifacts(jobId)}
					class="mt-1 rounded bg-red-600 px-3 py-1 text-xs font-medium text-white hover:bg-red-500"
				>
					Retry
				</button>
			</div>
		{:else if artifacts.length === 0}
			<p class="text-sm text-gray-500">No artifacts for this job.</p>
		{:else}
			<ul class="flex flex-col gap-2" role="list">
				{#each artifacts as artifact (artifact.filename)}
					<li class="flex items-center justify-between rounded-lg border border-gray-800 bg-gray-900 px-4 py-3">
						<div class="flex items-center gap-3 min-w-0">
							<!-- File icon -->
							<svg
								xmlns="http://www.w3.org/2000/svg"
								class="h-4 w-4 shrink-0 text-gray-500"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								stroke-width="2"
								stroke-linecap="round"
								stroke-linejoin="round"
								aria-hidden="true"
							>
								<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
								<polyline points="14 2 14 8 20 8" />
							</svg>
							<span class="min-w-0 truncate font-mono text-sm text-gray-200">
								{artifact.filename}
							</span>
						</div>
						<div class="flex items-center gap-4 shrink-0 ml-4">
							<span class="text-xs text-gray-500">{formatBytes(artifact.size_bytes)}</span>
							<a
								href="/api/v1/jobs/{jobId}/artifacts/{encodeURIComponent(artifact.filename)}"
								download={artifact.filename}
								class="flex items-center gap-1 rounded bg-gray-800 px-2 py-1 text-xs text-gray-300 hover:bg-gray-700 hover:text-white"
								aria-label="Download {artifact.filename}"
							>
								<svg
									xmlns="http://www.w3.org/2000/svg"
									class="h-3.5 w-3.5"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									stroke-width="2"
									stroke-linecap="round"
									stroke-linejoin="round"
									aria-hidden="true"
								>
									<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
									<polyline points="7 10 12 15 17 10" />
									<line x1="12" y1="15" x2="12" y2="3" />
								</svg>
								Download
							</a>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>
