const btn    = document.getElementById('submit');
const output = document.getElementById('output');

btn.addEventListener('click', async () => {
  const content = document.getElementById('content').value.trim();
  const prompt  = document.getElementById('prompt').value.trim();

  if (!content || !prompt) {
    output.textContent = 'Please fill in both the draft and the instruction.';
    return;
  }

  btn.disabled    = true;
  btn.textContent = 'Thinking…';
  output.textContent = '';

  try {
    const res  = await fetch('/api/complete', {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({ content, prompt }),
    });

    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Request failed');

    output.textContent = data.completion;
  } catch (err) {
    output.textContent = `Error: ${err.message}`;
  } finally {
    btn.disabled    = false;
    btn.textContent = 'Improve';
  }
});
