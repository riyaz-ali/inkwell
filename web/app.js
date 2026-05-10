// Inkwell frontend — streaming responses via native EventSource (article 5).
//
// The revision flow is two requests because the browser's EventSource client
// only supports GET. Adding a body to a streaming request requires either
// manual SSE parsing over fetch+ReadableStream, or this two-step protocol —
// we picked the latter so EventSource itself does the parsing, reconnect, and
// dev-tools integration for us.
//
// 1. POST /api/drafts/{id}/revisions      → { ticket: "<opaque>" }
// 2. EventSource /api/drafts/{id}/revisions/stream?ticket=<opaque>
//    emits `delta` events with text chunks, then a terminal `done` event,
//    or a `failure` event on application errors.
//
// app.js registers the Alpine component on alpine:init so the x-data="inkwell"
// binding on <body> resolves before Alpine walks the DOM.

document.addEventListener('alpine:init', () => {
  Alpine.data('inkwell', () => ({
    // --- state ---
    draftId: null,   // null until the first draft is persisted
    content: '',     // textarea content; locked after first submit
    prompt:  '',     // current instruction input
    mode:    'academic', // active writing mode
    busy:    false,  // true while a stream is in flight
    status:  '',     // feedback line below the controls
    isError: false,  // toggles .error on the status element
    thread:  [],     // [{ turn, prompt, mode, completion, streaming }, …]

    // locked becomes true after the first submit — the draft content is fixed
    // once it's been persisted; subsequent turns revise it, not replace it.
    get locked() { return this.draftId !== null; },

    // --- actions ---

    async submit() {
      const prompt  = this.prompt.trim();
      const content = this.content.trim();

      if (!prompt) {
        return this.setStatus('Enter an instruction before submitting.', true);
      }
      if (!this.locked && !content) {
        return this.setStatus('Paste or write a draft before submitting.', true);
      }

      this.busy = true;
      this.setStatus('');
      try {
        // First submission: persist the draft content so subsequent revisions
        // have something to anchor against. The server doesn't call Claude
        // here — creation is a pure storage operation.
        if (!this.locked) {
          const draft  = await this.createDraft(content);
          this.draftId = draft.draft_id;
        }

        // Stage the revision; receive a one-shot ticket.
        const { ticket } = await this.stageRevision(this.draftId, prompt, this.mode);

        // Push a placeholder turn into the thread so the user sees a target
        // for the streaming text to land in. We update its fields in place
        // as the stream produces deltas — Alpine's reactivity does the rest.
        //
        // Subtlety: Alpine 3 (via @vue/reactivity) wraps array elements in a
        // Proxy lazily, on read. The raw object we just pushed has no proxy,
        // so mutating the local `turn` variable would update memory without
        // tripping the proxy's set trap — and the x-text binding would
        // never re-render. We re-acquire the proxied reference from the
        // array before handing it to the streamer.
        const idx = this.thread.push({
          turn:       this.thread.length + 1, // optimistic; corrected on `done`
          prompt:     prompt,
          mode:       this.mode,
          completion: '',
          streaming:  true,
        }) - 1;
        const turn = this.thread[idx]; // proxied reference
        this.prompt = '';

        // Open the EventSource and stream into the placeholder turn.
        await this.streamTicket(this.draftId, ticket, turn);
      } catch (err) {
        this.setStatus(err.message, true);
      } finally {
        this.busy = false;
      }
    },

    setStatus(msg, error = false) {
      this.status  = msg;
      this.isError = error;
    },

    // --- API helpers ---

    async createDraft(content) {
      const res  = await fetch('/api/drafts', {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ content }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || 'Failed to create draft');
      return data;
    },

    async stageRevision(id, prompt, mode) {
      const res  = await fetch(`/api/drafts/${id}/revisions`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ prompt, mode }),
      });
      if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(text.trim() || 'Failed to stage revision');
      }
      return res.json();
    },

    // streamTicket opens an EventSource and updates `turn` in place as events
    // arrive. Resolves when `done` is observed; rejects on `failure` or any
    // connection-level error before `done`.
    streamTicket(id, ticket, turn) {
      return new Promise((resolve, reject) => {
        const url = `/api/drafts/${id}/revisions/stream?ticket=${encodeURIComponent(ticket)}`;
        const es  = new EventSource(url);
        let settled = false;

        // `delta` carries one chunk of generated text. We mutate the turn's
        // completion in place; Alpine re-renders the textnode each tick.
        es.addEventListener('delta', (e) => {
          const { text } = JSON.parse(e.data);
          turn.completion += text || '';
          this.$nextTick(() => {
            document.getElementById('thread').lastElementChild
              ?.scrollIntoView({ behavior: 'smooth', block: 'end' });
          });
        });

        // `done` is the happy-path terminator. We close the EventSource
        // synchronously here so the browser doesn't fire its own `error`
        // when the server closes the connection a moment later.
        es.addEventListener('done', (e) => {
          const data = JSON.parse(e.data);
          turn.streaming = false;
          if (typeof data.turn === 'number')        turn.turn = data.turn;
          if (typeof data.revision_id === 'number') turn.revisionId = data.revision_id;
          if (typeof data.mode === 'string')        turn.mode = data.mode;
          settled = true;
          es.close();
          resolve();
        });

        // `failure` is the application-level error. Distinct from EventSource's
        // built-in `error` event so the two can be handled independently.
        es.addEventListener('failure', (e) => {
          const data = (() => { try { return JSON.parse(e.data); } catch { return {}; } })();
          turn.streaming = false;
          settled = true;
          es.close();
          reject(new Error(data.error || 'stream failure'));
        });

        // Connection-level error — network drop, 4xx/5xx on the GET, or the
        // server closing without `done`. EventSource auto-reconnects on
        // generic errors, so we close explicitly to prevent it from looping
        // against an already-consumed ticket.
        es.addEventListener('error', () => {
          if (settled) return; // benign close after `done`/`failure`
          turn.streaming = false;
          settled = true;
          es.close();
          reject(new Error('connection lost; please retry'));
        });
      });
    },
  }));
});
