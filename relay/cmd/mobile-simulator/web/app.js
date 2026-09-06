const byId = (id) => document.getElementById(id);

async function request(path, body) {
  const response = await fetch(path, {
    method: body ? 'POST' : 'GET',
    headers: body ? {'content-type': 'application/json'} : {},
    body: body ? JSON.stringify(body) : undefined
  });
  const value = await response.json();
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`);
  return value;
}

async function refresh() {
  try {
    const status = await request('/api/status');
    const {events, ...summary} = status;
    byId('status').textContent = JSON.stringify(summary, null, 2);
    byId('events').textContent = JSON.stringify(events || [], null, 2);
  } catch (error) {
    byId('status').textContent = error.message;
  }
}

byId('pair').addEventListener('click', async () => {
  byId('status').textContent = '等待 Desktop 批准配对…';
  try {
    await request('/api/pair', {uri: byId('pair-uri').value.trim()});
    await refresh();
  } catch (error) {
    byId('status').textContent = error.message;
  }
});

byId('call').addEventListener('click', async () => {
  try {
    const params = JSON.parse(byId('params').value);
    const result = await request('/api/call', {method: byId('method').value.trim(), params});
    byId('result').textContent = JSON.stringify(result, null, 2);
  } catch (error) {
    byId('result').textContent = error.message;
  }
});

refresh();
setInterval(refresh, 2000);
