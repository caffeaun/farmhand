/**
 * FarmHand WebSocket client.
 *
 * Connects to ws://<host>/api/v1/ws.
 * Auto-reconnects on disconnect with exponential backoff (1s, 2s, 4s … max 30s).
 * Does NOT reconnect after an explicit close() call.
 *
 * Usage:
 *   const ws = new FarmhandWS((msg) => console.log(msg));
 *   ws.connect();
 *   // later:
 *   ws.close();
 */

import type { WSMessage } from '$lib/types';

const BACKOFF_INITIAL_MS = 1_000;
const BACKOFF_MAX_MS = 30_000;

function buildWsUrl(): string {
	if (typeof window === 'undefined') {
		// SSR — return a placeholder; connect() should only be called in the browser
		return 'ws://localhost:8080/api/v1/ws';
	}
	const protocol = location.protocol === 'https:' ? 'wss' : 'ws';
	return `${protocol}://${location.host}/api/v1/ws`;
}

export class FarmhandWS {
	private socket: WebSocket | null = null;
	private closed = false;
	private backoffMs = BACKOFF_INITIAL_MS;
	private retryTimer: ReturnType<typeof setTimeout> | null = null;
	private readonly url: string;
	readonly onMessage: (msg: WSMessage) => void;

	constructor(onMessage: (msg: WSMessage) => void, url?: string) {
		this.onMessage = onMessage;
		this.url = url ?? buildWsUrl();
	}

	/** Open the WebSocket connection. Call this once from the browser. */
	connect(): void {
		if (this.closed) return;

		this.socket = new WebSocket(this.url);

		this.socket.addEventListener('message', (ev: MessageEvent<string>) => {
			try {
				const msg = JSON.parse(ev.data) as WSMessage;
				this.onMessage(msg);
			} catch {
				// Ignore malformed / non-JSON frames
			}
		});

		this.socket.addEventListener('open', () => {
			// Reset backoff on successful connection
			this.backoffMs = BACKOFF_INITIAL_MS;
		});

		this.socket.addEventListener('close', () => {
			this.socket = null;
			if (!this.closed) {
				this.scheduleReconnect();
			}
		});

		this.socket.addEventListener('error', () => {
			// The 'close' event fires after 'error', so reconnect is handled there
		});
	}

	/** Close the connection permanently. No automatic reconnect will occur. */
	close(): void {
		this.closed = true;
		if (this.retryTimer !== null) {
			clearTimeout(this.retryTimer);
			this.retryTimer = null;
		}
		if (this.socket !== null) {
			this.socket.close();
			this.socket = null;
		}
	}

	private scheduleReconnect(): void {
		const delay = this.backoffMs;
		// Exponential backoff capped at BACKOFF_MAX_MS
		this.backoffMs = Math.min(this.backoffMs * 2, BACKOFF_MAX_MS);
		this.retryTimer = setTimeout(() => {
			this.retryTimer = null;
			this.connect();
		}, delay);
	}
}
