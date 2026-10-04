let csrf='';
let socket=null;
let socketPromise=null;
let sequence=0;
let pairQrObjectUrl='';
let pairQrGeneration=0;
let remoteErrorText='';
let remoteIdentity=null;
const remoteMessages={relay_upgrade_required:'Relay 版本不支持自动注册，请升级 Relay 或使用旧版邀请码注册。',invite_required:'此 Relay 需要邀请码，请输入后完成注册。',invalid_invite:'邀请码无效或已过期。',enrollment_disabled:'Relay 已暂停新设备注册。',device_revoked:'设备已被 Relay 撤销，请联系部署者。',identity_conflict:'设备身份或 Relay 地址不匹配，请检查地址；需要恢复时可明确重置远程身份。',auth_failed:'设备认证失败，请检查 Relay 配置或联系部署者。',daemon_capacity_reached:'Relay 登记设备数量已达上限，请联系部署者。',rate_limited:'请求过于频繁，稍后将重试。',temporarily_unavailable:'暂时无法连接 Relay，将自动重试。'};
const pending=new Map();
const q=(id)=>document.getElementById(id);

async function exchange(){
  const token=new URLSearchParams(location.hash.slice(1)).get('token');
  if(token){
    const r=await fetch('/auth/exchange',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({token})});
    if(!r.ok)throw new Error('管理链接已失效，请重新运行 work-anywhere ui');
    csrf=(await r.json()).csrf;
    history.replaceState(null,'',location.pathname);
  }else{csrf=sessionStorage.getItem('csrf')||'';}
  if(!csrf)throw new Error('缺少管理会话，请重新运行 work-anywhere ui');
  sessionStorage.setItem('csrf',csrf);
}

function openSocket(){
  if(socket&&socket.readyState===WebSocket.OPEN)return Promise.resolve(socket);
  if(socketPromise)return socketPromise;
  socketPromise=new Promise((resolve,reject)=>{
    const scheme=location.protocol==='https:'?'wss':'ws';
    const value=new WebSocket(`${scheme}://${location.host}/api/ws?csrf=${encodeURIComponent(csrf)}`);
    socket=value;
    value.onopen=()=>{socketPromise=null;resolve(value);};
    value.onmessage=(event)=>{
      let message;
      try{message=JSON.parse(event.data);}catch{return;}
      if(message.type==='response'&&pending.has(message.id)){
        const waiter=pending.get(message.id);pending.delete(message.id);
        if(message.error){const error=new Error(message.error.message||'daemon 请求失败');error.code=message.error.code;waiter.reject(error);}
        else waiter.resolve(message.result);
      }else if(message.type==='event'){
        refresh().catch(()=>{});
      }
    };
    value.onclose=()=>{
      if(socket===value)socket=null;
      for(const waiter of pending.values())waiter.reject(new Error('管理连接已断开'));
      pending.clear();
    };
    value.onerror=()=>{if(socketPromise){socketPromise=null;reject(new Error('无法连接 daemon 管理接口'));}};
  });
  return socketPromise;
}

async function call(method,params={}){
  const connection=await openSocket();
  const id=`ui-${Date.now()}-${++sequence}`;
  return new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>{pending.delete(id);reject(new Error('daemon 请求超时'));},30000);
    pending.set(id,{resolve:(value)=>{clearTimeout(timer);resolve(value);},reject:(error)=>{clearTimeout(timer);reject(error);}});
    connection.send(JSON.stringify({type:'request',id,method,params}));
  });
}

async function refresh(){
  const r=await fetch('/api/status',{headers:{'X-WA-CSRF':csrf}});
  if(!r.ok)throw new Error('管理会话无效，请重新打开管理页');
  const body=await r.json();
  if(body.error)throw new Error(body.error.message);
  const s=body.result;
  q('daemon').textContent='运行中';
  q('pid').textContent=s.pid;
  q('uptime').textContent=Math.floor(s.uptimeSeconds/60)+' 分钟';
  q('count').textContent=s.instances.length;
  q('instances').innerHTML=s.instances.map(i=>`<article class="card"><div><h3>${escapeHtml(i.name||i.id.slice(0,12))}</h3><div class="meta">${escapeHtml(i.codexHome)} · ${escapeHtml(i.runtime.version||'未知版本')}</div></div><div class="state">${escapeHtml(i.state)}</div></article>`).join('')||'<div class="card meta">等待 ChatGPT Desktop 连接…</div>';
  renderRemote(s.remote||{});
}

function renderRemote(remote){
  const identity=`${remote.relayUrl||''}:${remote.deviceId||''}`;
  if(remoteIdentity!==null&&remoteIdentity!==identity){q('pair-uri').value='';renderPairQR('').catch(showError);}
  remoteIdentity=identity;
  if(document.activeElement!==q('relay-url'))q('relay-url').value=remote.relayUrl||'';
  q('relay-connection').textContent=remote.connected?'已连接':'未连接';
  q('relay-connection').className=remote.connected?'state':'state warn';
  q('relay-fingerprint').textContent=remote.fingerprint||'—';
  q('relay-device').textContent=remote.deviceId||'—';
  const phases={unconfigured:'请填写 Relay 地址',disabled:'远程访问已停用',pending_registration:'等待注册',registering:'正在注册',invite_required:'请输入邀请码完成注册',connecting:'正在连接',connected:'已连接，可以配对手机',needs_attention:'连接需要处理，请重试或检查配置'};
  q('relay-progress').textContent=(phases[remote.phase]||'')+(remote.nextRetryAt&&Date.parse(remote.nextRetryAt)>Date.now()?`，下次重试：${new Date(remote.nextRetryAt).toLocaleTimeString()}`:'');
  q('relay-token-label').hidden=remote.registrationMode!=='invite'&&remote.lastErrorCode!=='relay_upgrade_required';
  q('relay-legacy').hidden=remote.lastErrorCode!=='relay_upgrade_required';
  q('pair-start').disabled=!remote.connected;
  if(remote.lastError){remoteErrorText=remoteMessages[remote.lastErrorCode]||remote.lastError;q('error').textContent=remoteErrorText;}else if(q('error').textContent===remoteErrorText){q('error').textContent='';remoteErrorText='';}
  const attempts=Array.isArray(remote.pairAttempts)?remote.pairAttempts:[];
  if(!q('pair-uri').value&&attempts[0]&&attempts[0].uri){q('pair-uri').value=attempts[0].uri;renderPairQR(attempts[0].uri).catch(showError);}
  q('pair-attempts').innerHTML=attempts.map(attempt=>{
    const identified=attempt.mobileDeviceId||attempt.mobileDisplayName;
    const label=identified?`移动端：${escapeHtml(attempt.mobileDisplayName||attempt.mobileDeviceId)}`:'等待移动端扫码/粘贴 URI';
    const buttons=attempt.decisionSubmitted?'<span class="state">已提交，等待 Relay 确认</span>':identified?`<button data-pair-action="approve" data-pair-id="${escapeHtml(attempt.id)}" class="primary">批准</button><button data-pair-action="reject" data-pair-id="${escapeHtml(attempt.id)}">拒绝</button>`:`<button data-pair-action="cancel" data-pair-id="${escapeHtml(attempt.id)}">取消</button>`;
    return `<article class="card"><div><h3>${escapeHtml(label)}</h3><div class="meta">${escapeHtml(attempt.id)} · 截止 ${escapeHtml(new Date(attempt.expiresAt).toLocaleString())}</div></div><div class="actions compact">${buttons}</div></article>`;
  }).join('')||'<div class="card meta">暂无待处理配对</div>';
  const pairings=Array.isArray(remote.pairings)?remote.pairings:[];
  q('pairings').innerHTML=pairings.map(pairing=>`<article class="card"><div><h3>${escapeHtml(pairing.displayName||pairing.mobileDeviceId)}</h3><div class="meta">权限：${escapeHtml((pairing.scopes||[]).join(', '))}</div></div><button data-pair-action="revoke" data-pair-id="${escapeHtml(pairing.id)}">撤销</button></article>`).join('')||'<div class="card meta">暂无已授权移动端</div>';
}

async function renderPairQR(uri){
  const generation=++pairQrGeneration;
  if(!uri){if(pairQrObjectUrl)URL.revokeObjectURL(pairQrObjectUrl);pairQrObjectUrl='';q('pair-qr').removeAttribute('src');q('pair-qr-wrap').hidden=true;return;}
  const response=await fetch('/api/pair-qr',{method:'POST',headers:{'content-type':'application/json','X-WA-CSRF':csrf},body:JSON.stringify({uri})});
  if(generation!==pairQrGeneration)return;
  if(!response.ok)throw new Error('无法生成配对二维码');
  const blob=await response.blob();
  if(generation!==pairQrGeneration)return;
  if(pairQrObjectUrl)URL.revokeObjectURL(pairQrObjectUrl);
  pairQrObjectUrl=URL.createObjectURL(blob);
  q('pair-qr').src=pairQrObjectUrl;
  q('pair-qr-wrap').hidden=false;
}

function escapeHtml(v){return String(v).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'}[c]));}
function showError(error){q('error').textContent=remoteMessages[error.code]||error.message||String(error);}
async function action(fn){q('error').textContent='';try{await fn();await refresh();}catch(error){showError(error);}}

q('refresh').onclick=()=>action(async()=>{});
q('relay-configure').onclick=()=>action(async()=>{const params={relayUrl:q('relay-url').value.trim(),displayName:q('relay-name').value.trim(),enabled:true};try{await call('remote.configure',params);}catch(error){if(error.code!=='relay_change_requires_reset')throw error;if(!confirm('更换 Relay 将清除当前远程身份和手机配对，继续吗？'))return;await call('remote.configure',{...params,resetIdentity:true});q('pair-uri').value='';await renderPairQR('');}});
q('relay-enroll').onclick=()=>action(async()=>{try{await call('remote.register',{token:q('relay-token').value,displayName:q('relay-name').value.trim()});}finally{q('relay-token').value='';}});
q('relay-legacy').onclick=()=>action(async()=>{try{await call('remote.enroll',{token:q('relay-token').value,displayName:q('relay-name').value.trim()});}finally{q('relay-token').value='';}});
q('relay-reset').onclick=()=>{if(!confirm('重置会删除远程身份和所有手机配对，需要重新扫码，继续吗？'))return;action(async()=>{await call('remote.configure',{relayUrl:q('relay-url').value.trim(),enabled:true,resetIdentity:true});q('pair-uri').value='';await renderPairQR('');});};
q('relay-reconnect').onclick=()=>action(()=>call('remote.reconnect'));
q('pair-start').onclick=()=>action(async()=>{const result=await call('remote.pair.start',{ttlSeconds:300});if(result&&result.uri){q('pair-uri').value=result.uri;await renderPairQR(result.uri);}});
q('pair-refresh').onclick=()=>action(()=>call('remote.pair.list'));
q('pair-copy').onclick=async()=>{try{await navigator.clipboard.writeText(q('pair-uri').value);q('error').textContent='配对 URI 已复制';}catch(error){showError(new Error('复制失败，请手动复制 URI'));}};
q('pair-attempts').onclick=(event)=>{const button=event.target.closest('[data-pair-action]');if(!button)return;const id=button.dataset.pairId;const operation=button.dataset.pairAction;action(async()=>{if(operation==='approve'||operation==='reject'){if(operation==='reject'&&!confirm('确定拒绝这个移动端配对请求吗？'))return;await call('remote.pair.resolve',{pairingId:id,approved:operation==='approve',scopes:['status.read','conversation.read','turn.start','turn.interrupt','action.read','action.resolve','attachment.write','catalog.read']});}else if(operation==='cancel'){if(!confirm('确定取消这个待处理的配对请求吗？'))return;await call('remote.pair.cancel',{pairingId:id});}});};
q('pairings').onclick=(event)=>{const button=event.target.closest('[data-pair-action="revoke"]');if(!button)return;if(!confirm('撤销后该移动端将无法继续访问此 Desktop，确定继续吗？'))return;action(()=>call('remote.pair.revoke',{pairingId:button.dataset.pairId}));};

async function run(){try{await exchange();await refresh();openSocket().catch(showError);setInterval(()=>refresh().catch(showError),5000);}catch(error){showError(error);q('daemon').textContent='未连接';}}
run();
