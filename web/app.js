// Inkwell frontend — streaming responses (article 5).
//
// State lives in the Alpine component below. Alpine binds it to the DOM
// declaratively via x-model, x-for, x-text, and @click — no manual DOM
// queries or element manipulation needed.
//
// app.js registers the component before Alpine initialises (alpine:init fires
// before Alpine walks the DOM), so the x-data="inkwell" attribute on <body>
// resolves correctly.
//
// The revision endpoint streams Server-Sent Events: each `delta` event carries
// a chunk of generated text, and a terminal `done` event carries the persisted
// revision id. We push a placeholder turn into the thread on submit and append
// to its `completion` as deltas arrive — Alpine re-renders the textnode each
// tick, producing the live typewriter effect.

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

        // Push a placeholder turn into the thread so the user sees a target
        // for the streaming text to land in. We update its fields in place
        // as the stream produces deltas — Alpine's reactivity does the rest.
        const turn = {
          turn:       this.thread.length + 1, // optimistic; corrected on `done`
          prompt:     prompt,
          mode:       this.mode,
          completion: '',
          streaming:  true,
        };
        this.thread.push(turn);
        this.prompt = '';

        await this.streamRevision(this.draftId, prompt, this.mode, turn);
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

    // streamRevision opens a streaming POST and mutates `turn` in place as
    // events arrive. The server emits three event kinds: `delta` carries a
    // text chunk, `done` carries the persisted metadata, `error` carries a
    // failure message. The function resolves when the stream closes cleanly
    // and rejects if any error event is observed.
    async streamRevision(id, prompt, mode, turn) {
      const res = await fetch(`/api/drafts/${id}/revisions`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
        body:    JSON.stringify({ prompt, mode }),
      });

      // 4xx/5xx responses come back as plain JSON before the SSE stream
      // starts, so we can read them with .json() like any other failure.
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || 'Revision request failed');
      }

      const reader  = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      // SSE frames are delimited by a blank line (\n\n). We accumulate raw
      // chunks until we have at least one complete frame, then parse it.
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });

        let idx;
        while ((idx = buffer.indexOf('\n\n')) !== -1) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          this.handleSSEFrame(frame, turn);
        }
      }

      // Trailing data after the last blank line, if any. With a well-behaved
      // server this is empty — but parse it anyway so a missing final \n\n
      // doesn't lose the `done` event.
      if (buffer.trim()) this.handleSSEFrame(buffer, turn);
    },

    // handleSSEFrame parses one `event:` / `data:` block and applies it.
    handleSSEFrame(frame, turn) {
      let event = 'message';
      let data  = '';
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data += line.slice(5).trimStart();
      }

      let payload = {};
      try { payload = data ? JSON.parse(data) : {}; }
      catch { return; }

      if (event === 'delta') {
        turn.completion += payload.text || '';
        // Keep the streaming turn pinned to view as it grows.
        this.$nextTick(() => {
          document.getElementById('thread').lastElementChild
            ?.scrollIntoView({ behavior: 'smooth', block: 'end' });
        });
      } else if (event === 'done') {
        turn.streaming = false;
        if (typeof payload.turn === 'number')        turn.turn = payload.turn;
        if (typeof payload.revision_id === 'number') turn.revisionId = payload.revision_id;
        if (typeof payload.mode === 'string')        turn.mode = payload.mode;
      } else if (event === 'error') {
        turn.streaming = false;
        throw new Error(payload.error || 'stream error');
      }
    },
  }));
});
