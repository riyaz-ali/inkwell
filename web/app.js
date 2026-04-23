// Inkwell frontend — writing modes (article 4).
//
// State lives in the Alpine component below. Alpine binds it to the DOM
// declaratively via x-model, x-for, x-text, and @click — no manual DOM
// queries or element manipulation needed.
//
// app.js registers the component before Alpine initialises (alpine:init fires
// before Alpine walks the DOM), so the x-data="inkwell" attribute on <body>
// resolves correctly.

document.addEventListener('alpine:init', () => {
  Alpine.data('inkwell', () => ({
    // --- state ---
    draftId: null,   // null until the first draft is persisted
    content: '',     // textarea content; locked after first submit
    prompt:  '',     // current instruction input
    mode:    'academic', // active writing mode
    busy:    false,  // true while an API call is in flight
    status:  '',     // feedback line below the controls
    isError: false,  // toggles .error on the status element
    thread:  [],     // [{ turn, prompt, mode, completion }, …]

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
      try {
        // First submission: persist the draft content so subsequent revisions
        // have something to anchor against. The server doesn't call Claude
        // here — creation is a pure storage operation.
        if (!this.locked) {
          const draft  = await this.createDraft(content);
          this.draftId = draft.draft_id;
        }

        const revision = await this.requestRevision(this.draftId, prompt, this.mode);
        this.thread.push(revision);
        this.prompt = '';
        this.setStatus('');

        // Scroll the new turn into view after Alpine renders it.
        this.$nextTick(() => {
          const threadEl = document.getElementById('thread');
          threadEl.lastElementChild?.scrollIntoView({ behavior: 'smooth', block: 'end' });
        });
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

    async requestRevision(id, prompt, mode) {
      const res  = await fetch(`/api/drafts/${id}/revisions`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ prompt, mode }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || 'Revision request failed');
      return data;
    },
  }));
});
