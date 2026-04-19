// Inkwell frontend — writing modes (article 4).
//
// State: a single in-memory draft id. Null until the user has submitted their
// first instruction, at which point we POST /api/drafts to persist the
// initial content and remember the returned id. Every subsequent submit hits
// POST /api/drafts/{id}/revisions with the current writing mode and appends
// the returned revision to the visible thread.
//
// The writing mode (academic / journalist / engineer) is picked up from the
// radio group on each submit — the user can switch persona between turns.

const contentEl = document.getElementById('content');
const promptEl  = document.getElementById('prompt');
const submitEl  = document.getElementById('submit');
const threadEl  = document.getElementById('thread');
const statusEl  = document.getElementById('status');

let draftId = null;

submitEl.addEventListener('click', async () => {
  const prompt  = promptEl.value.trim();
  const content = contentEl.value.trim();
  const mode    = selectedMode();

  if (!prompt) {
    setStatus('Enter an instruction before submitting.', true);
    return;
  }
  if (draftId === null && !content) {
    setStatus('Paste or write a draft before submitting.', true);
    return;
  }

  setBusy(true);
  try {
    // First submission: persist the draft content so subsequent revisions
    // have something to anchor against. The server doesn't call Claude here
    // — creation is a pure storage operation.
    if (draftId === null) {
      const draft = await createDraft(content);
      draftId = draft.draft_id;
      contentEl.disabled = true;
    }

    const revision = await requestRevision(draftId, prompt, mode);
    appendTurn(prompt, revision.completion, revision.mode, revision.turn);
    promptEl.value = '';
    setStatus('');
  } catch (err) {
    setStatus(err.message, true);
  } finally {
    setBusy(false);
    promptEl.focus();
  }
});

// selectedMode returns the value of the checked mode radio button.
function selectedMode() {
  const checked = document.querySelector('input[name="mode"]:checked');
  return checked ? checked.value : 'academic';
}

async function createDraft(content) {
  const res  = await fetch('/api/drafts', {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body:    JSON.stringify({ content }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Failed to create draft');
  return data;
}

async function requestRevision(id, prompt, mode) {
  const res  = await fetch(`/api/drafts/${id}/revisions`, {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body:    JSON.stringify({ prompt, mode }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'Revision request failed');
  return data;
}

function appendTurn(prompt, completion, mode, turn) {
  const wrap = document.createElement('div');
  wrap.className = 'turn';

  const meta = document.createElement('div');
  meta.className = 'meta';

  const p = document.createElement('span');
  p.className = 'prompt';
  p.textContent = `Turn ${turn}: ${prompt}`;

  const badge = document.createElement('span');
  badge.className = 'mode-badge';
  badge.textContent = mode;

  meta.append(p, badge);

  const c = document.createElement('div');
  c.className = 'completion';
  c.textContent = completion;

  wrap.append(meta, c);
  threadEl.append(wrap);
  wrap.scrollIntoView({ behavior: 'smooth', block: 'end' });
}

function setBusy(busy) {
  submitEl.disabled = busy;
  submitEl.textContent = busy ? 'Thinking…' : 'Improve';
}

function setStatus(msg, isError = false) {
  statusEl.textContent = msg;
  statusEl.classList.toggle('error', isError);
}
