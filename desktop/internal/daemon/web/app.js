let csrf='';
let socket=null;
let socketPromise=null;
let sequence=0;
let pairQrObjectUrl='';
let pairQrGeneration=0;
let remoteErrorShown=null;
let remoteIdentity=null;
let remotePhase='';
let pollTimer=null;
let pollErrorShown='';
let sessionLost=false;
const remoteMessages={relay_upgrade_required:'Relay 版本不支持自动注册，请升级 Relay 或使用旧版邀请码注册。',invite_required:'此 Relay 需要邀请码，请填写邀请码后点击“启用远程访问”。',invalid_invite:'邀请码无效或已过期。',enrollment_disabled:'Relay 已暂停新设备注册。',device_revoked:'设备已被 Relay 撤销，请联系部署者。',identity_conflict:'设备身份或 Relay 地址不匹配，请检查地址；需要恢复时可明确重置远程身份。',auth_failed:'设备认证失败，请检查 Relay 配置或联系部署者。',daemon_capacity_reached:'Relay 登记设备数量已达上限，请联系部署者。',rate_limited:'请求过于频繁，稍后将重试。',temporarily_unavailable:'暂时无法连接 Relay，将自动重试。'};
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
  if(sessionLost)throw sessionExpiredError();
  const connection=await openSocket();
  const id=`ui-${Date.now()}-${++sequence}`;
  return new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>{pending.delete(id);reject(new Error('daemon 请求超时'));},30000);
    pending.set(id,{resolve:(value)=>{clearTimeout(timer);resolve(value);},reject:(error)=>{clearTimeout(timer);reject(error);}});
    connection.send(JSON.stringify({type:'request',id,method,params}));
  });
}

function codedError(message,code){const error=new Error(message);error.code=code;return error;}
const sessionExpiredError=()=>codedError('管理会话已失效，请从托盘菜单“打开管理页”或运行 work-anywhere ui 重新打开。','admin_session_expired');

async function refresh(){
  if(sessionLost)throw sessionExpiredError();
  let r;
  try{r=await fetch('/api/status',{headers:{'X-WA-CSRF':csrf}});}
  catch{throw codedError('无法连接 daemon，它可能已退出或重启；请从托盘菜单重新打开管理页。','daemon_unreachable');}
  if(r.status===401)throw sessionExpiredError();
  if(!r.ok)throw new Error(`daemon 状态请求失败（HTTP ${r.status}）`);
  const body=await r.json();
  if(body.error)throw new Error(body.error.message);
  const s=body.result;
  q('daemon').textContent='运行中';
  q('pid').textContent=s.pid;
  q('uptime').textContent=Math.floor(s.uptimeSeconds/60)+' 分钟';
  const agents=(Array.isArray(s.agents)?s.agents:[]).filter(a=>a.kind==='claude-code');
  q('count').textContent=s.instances.length+agents.filter(a=>a.id).length;
  q('claude-agents').innerHTML=agents.map(renderClaudeAgent).join('')||'<div class="card meta">未检测到 Claude Code</div>';
  q('instances').innerHTML=s.instances.map(i=>`<article class="card"><div><h3>${escapeHtml(i.name||i.id.slice(0,12))}</h3><div class="meta">${escapeHtml(i.codexHome)} · ${escapeHtml(i.runtime.version||'未知版本')}</div></div><div class="state">${escapeHtml(i.state)}</div></article>`).join('')||'<div class="card meta">未连接 ChatGPT Desktop（可选）</div>';
  renderStartup(s.startup||{});
  renderRemote(s.remote||{});
  renderUpdate(s.update);
}

const claudeReasons={disabled:'已在设置中停用',not_found:'未找到 Claude Code 配置目录，请先安装并登录 Claude Code',start_failed:'启动失败'};
function renderClaudeAgent(agent){
  const claude=agent.claude||{};
  const details=[agent.configDir,claude.runtimeVersion?`Claude ${claude.runtimeVersion}`:(claude.runtimeError?`运行时不可用：${claude.runtimeError}`:'')].filter(Boolean).map(escapeHtml).join(' · ');
  const activity=agent.id?`活跃 Turn：${agent.activeTurn?'是':'否'} · 待处理审批：${agent.pendingActions||0}`:'';
  const problem=[agent.reason?(claudeReasons[agent.reason]||agent.reason):'',agent.error||claude.lastProcessError||''].filter(Boolean).map(escapeHtml).join('：');
  const ready=agent.id&&agent.status==='ready'&&!agent.reason;
  return `<article class="card"><div><h3>${escapeHtml(agent.name||'Claude Code')}</h3><div class="meta">${details||'—'}</div>${activity?`<div class="meta">${escapeHtml(activity)}</div>`:''}${problem?`<div class="meta warn-text">${problem}</div>`:''}</div><div class="state${ready?'':' warn'}">${escapeHtml(agent.id?agent.status:'未启用')}</div></article>`;
}

function renderStartup(startup){
  const autostart=startup.autostart||{};
  const toggle=q('autostart');
  toggle.disabled=!autostart.supported;
  toggle.checked=!!autostart.enabled;
  q('autostart-meta').textContent=startup.autostartError?`无法读取自启动设置：${startup.autostartError}`:!autostart.supported?'当前平台不支持自启动':autostart.enabled&&!autostart.current?'自启动指向其他位置的 work-anywhere.exe，取消后重新勾选即可指向当前程序':'';
  const codex=startup.codexIntegration||{};
  q('codex-integration').textContent=!codex.supported?'':codex.installed?'ChatGPT Codex Desktop 集成：已启用（CODEX_CLI_PATH 指向当前程序）':codex.codexCliPath?`ChatGPT Codex Desktop 集成：CODEX_CLI_PATH 指向其他程序（${codex.codexCliPath}）`:'ChatGPT Codex Desktop 集成：未启用，可运行 work-anywhere install 启用';
}

const updateMessages={update_manifest_unavailable:'无法获取更新信息，请检查网络或镜像地址。',update_manifest_invalid:'更新清单格式无效。',update_signature_invalid:'更新清单签名校验失败，已拒绝。',update_hash_mismatch:'安装包校验失败，已删除，请重试。',update_download_failed:'下载安装包失败，可稍后重试，已下载部分会续传。',update_no_asset:'新版本没有适用于本机的安装包。',update_unsupported_install:'便携版不能自动更新，请从 GitHub Releases 下载新版本。',update_active_turns:'有正在运行的 Turn。',update_blocked_by_bridge:'ChatGPT Desktop 正在使用 Work Anywhere，请完全退出 ChatGPT Desktop 后重试。',update_in_progress:'更新已在进行中。',update_install_failed:'上次升级未完成，已恢复旧版本。'};
const updateStates={checking:'正在检查',upToDate:'已是最新',available:'可更新',downloading:'正在下载',downloaded:'已下载',applying:'正在安装',error:'需要处理'};
let updateNoticeShown=false;
function updateMessage(error){return error?(updateMessages[error.code]||error.message||error.code):'';}
function formatSize(size){return size>0?`${(size/1048576).toFixed(1)} MiB`:'';}
function renderUpdate(update){
  const banner=q('update-banner');
  if(!update){banner.hidden=true;q('update-check-meta').textContent='';return;}
  q('update-auto').checked=!!update.autoCheck;
  if(document.activeElement!==q('update-mirror'))q('update-mirror').value=update.mirrorBase||'';
  const checked=update.lastCheckedAt?`上次检查：${new Date(update.lastCheckedAt).toLocaleString()}`:'尚未检查';
  const kind=update.installKind==='portable'?'便携版，不能自动更新':'';
  q('update-check-meta').textContent=[`当前版本 ${update.currentVersion}`,update.state==='upToDate'?'已是最新':'',checked,kind].filter(Boolean).join(' · ');
  if(update.updatedFrom&&!updateNoticeShown){updateNoticeShown=true;notify(`已从 ${update.updatedFrom} 更新到 ${update.currentVersion}`,'success');}
  const latest=update.latest;
  const state=update.state;
  const active=['available','downloading','downloaded','applying'].includes(state)||(state==='error'&&(latest||update.error));
  banner.hidden=!active;
  if(!active)return;
  q('update-title').textContent=latest?`发现新版本 ${latest.version}`:'更新失败';
  q('update-meta').textContent=[`当前版本 ${update.currentVersion}`,latest?formatSize(latest.size):'',latest&&!update.canApply?(update.installKind==='portable'?'便携版请手动下载':'开发版本不能自动更新'):''].filter(Boolean).join(' · ');
  q('update-state').textContent=updateStates[state]||state;
  q('update-state').className=state==='error'?'state warn':'state';
  const progress=q('update-progress');
  const total=update.progress&&update.progress.total;
  progress.hidden=state!=='downloading'||!total;
  if(total)progress.value=Math.floor(update.progress.received*100/total);
  const message=updateMessage(update.error);
  q('update-error').textContent=message+(update.error&&update.error.logPath?`（安装日志：${update.error.logPath}）`:'');
  q('update-error').hidden=!message;
  const canAct=!!latest&&update.canApply;
  q('update-apply').hidden=!canAct||state==='downloading'||state==='applying';
  q('update-apply').textContent=state==='downloaded'?'立即安装':state==='error'?'重试更新':'立即更新';
  q('update-skip').hidden=!latest||!['available','error'].includes(state);
  q('update-cancel').hidden=state!=='downloading';
  const notes=q('update-notes');
  const notesUrl=latest&&typeof latest.notesUrl==='string'&&latest.notesUrl.startsWith('https://')?latest.notesUrl:'';
  notes.hidden=!notesUrl;
  if(notesUrl)notes.href=notesUrl;
}
async function applyUpdate(){
  const status=await call('system.update.status');
  if(!status.latest)throw new Error('没有可安装的新版本');
  const version=status.latest.version;
  try{await call('system.update.apply',{version});}
  catch(error){
    if(error.code==='update_active_turns'){
      if(!confirm(`${error.message}\n更新会中断正在运行的 Turn，确定继续吗？`))return;
      await call('system.update.apply',{version,confirmInterrupt:true});
    }else throw error;
  }
  notify('正在更新 Work Anywhere；下载完成后会自动安装并重启，完成后请重新运行 work-anywhere ui 打开管理页。','info');
}

function renderRemote(remote){
  const identity=`${remote.relayUrl||''}:${remote.deviceId||''}`;
  if(remoteIdentity!==null&&remoteIdentity!==identity){q('pair-uri').value='';renderPairQR('').catch(showError);}
  remoteIdentity=identity;
  remotePhase=remote.phase||'';
  if(document.activeElement!==q('relay-url'))q('relay-url').value=remote.relayUrl||'';
  q('relay-connection').textContent=remote.connected?'已连接':'未连接';
  q('relay-connection').className=remote.connected?'state':'state warn';
  q('relay-fingerprint').textContent=remote.fingerprint||'—';
  q('relay-device').textContent=remote.deviceId||'—';
  const phases={unconfigured:'请填写 Relay 地址',disabled:'远程访问已停用',pending_registration:'等待注册',registering:'正在注册',invite_required:'请填写邀请码后点击“启用远程访问”',connecting:'正在连接',connected:'已连接，可以配对手机',needs_attention:'连接需要处理，请重试或检查配置'};
  q('relay-progress').textContent=(phases[remote.phase]||'')+(remote.nextRetryAt&&Date.parse(remote.nextRetryAt)>Date.now()?`，下次重试：${new Date(remote.nextRetryAt).toLocaleTimeString()}`:'');
  q('relay-token-label').hidden=remote.registrationMode!=='invite'&&remote.lastErrorCode!=='relay_upgrade_required';
  q('relay-legacy').hidden=remote.lastErrorCode!=='relay_upgrade_required';
  q('pair-start').disabled=!remote.connected;
  const remoteError=remote.lastError?(remoteMessages[remote.lastErrorCode]||remote.lastError):'';
  q('relay-error').textContent=remoteError;
  q('relay-error').hidden=!remoteError;
  // Pop up only when the error changes, not on every periodic refresh, and
  // withdraw the previous popup once the Relay state moves on.
  if(remoteErrorShown&&remoteErrorShown!==remoteError)dismiss(remoteErrorShown);
  if(remoteError&&remoteError!==remoteErrorShown)notify(remoteError);
  remoteErrorShown=remoteError;
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
// notify shows a popup at the top of the page. Errors stay until closed so they
// are not missed; other notices close themselves. Identical visible notices are
// not stacked.
function notify(message,kind='error'){
  if(!message)return;
  const box=q('toasts');
  for(const existing of box.children){if(existing.dataset.message===message)existing.remove();}
  const toast=document.createElement('div');
  toast.className=`toast ${kind}`;
  toast.dataset.message=message;
  toast.setAttribute('role',kind==='error'?'alert':'status');
  const text=document.createElement('span');
  text.textContent=message;
  const close=document.createElement('button');
  close.type='button';close.className='toast-close';close.textContent='×';close.setAttribute('aria-label','关闭提示');
  close.onclick=()=>toast.remove();
  toast.append(text,close);
  box.append(toast);
  while(box.children.length>4)box.firstElementChild.remove();
  if(kind!=='error')setTimeout(()=>toast.remove(),4000);
}
function dismiss(message){
  if(!message)return;
  for(const toast of [...q('toasts').children]){if(toast.dataset.message===message)toast.remove();}
}

// Periodic refresh failures pop up once per distinct message. An expired
// session cannot recover in this page, so polling stops instead of
// re-raising the same error every few seconds.
function refreshFailed(error){
  if(error.code==='admin_session_expired'){
    if(sessionLost)return;
    sessionLost=true;
    if(pollTimer){clearInterval(pollTimer);pollTimer=null;}
    if(socket)socket.close();
    dismiss(pollErrorShown);pollErrorShown='';
    q('daemon').textContent='会话已失效';
    notify(error.message);
    return;
  }
  if(error.code==='daemon_unreachable')q('daemon').textContent='未连接';
  if(error.message!==pollErrorShown){dismiss(pollErrorShown);pollErrorShown=error.message;notify(error.message);}
}
async function poll(){
  try{await refresh();dismiss(pollErrorShown);pollErrorShown='';}
  catch(error){refreshFailed(error);}
}

function showError(error){if(error&&error.code==='admin_session_expired'){refreshFailed(error);return;}notify(remoteMessages[error.code]||(error.code==='update_active_turns'?'':updateMessages[error.code])||error.message||String(error));}
async function action(fn){try{await fn();await refresh();}catch(error){showError(error);}}

q('refresh').onclick=()=>action(async()=>{});
q('autostart').onchange=(event)=>{const enabled=event.target.checked;action(()=>call('system.autostart.set',{enabled})).then(()=>refresh().catch(()=>{}));};
// One button saves the Relay address and registers; when the Relay requires an
// invite code it is submitted in the same step.
q('relay-configure').onclick=()=>action(async()=>{
  const params={relayUrl:q('relay-url').value.trim(),displayName:q('relay-name').value.trim(),enabled:true};
  const token=q('relay-token-label').hidden?'':q('relay-token').value.trim();
  if(!q('relay-token-label').hidden&&!token&&remotePhase==='invite_required'){q('relay-token').focus();throw new Error('请先填写邀请码');}
  try{await call('remote.configure',params);}catch(error){if(error.code!=='relay_change_requires_reset')throw error;if(!confirm('更换 Relay 将清除当前远程身份和手机配对，继续吗？'))return;await call('remote.configure',{...params,resetIdentity:true});q('pair-uri').value='';await renderPairQR('');}
  if(token){try{await call('remote.register',{token,displayName:params.displayName});}finally{q('relay-token').value='';}}
  notify(token?'邀请码已提交，正在连接 Relay':'Relay 设置已保存，正在注册和连接','success');
});
q('relay-legacy').onclick=()=>action(async()=>{try{await call('remote.enroll',{token:q('relay-token').value.trim(),displayName:q('relay-name').value.trim()});}finally{q('relay-token').value='';}});
q('relay-reset').onclick=()=>{if(!confirm('重置会删除远程身份和所有手机配对，需要重新扫码，继续吗？'))return;action(async()=>{await call('remote.configure',{relayUrl:q('relay-url').value.trim(),enabled:true,resetIdentity:true});q('pair-uri').value='';await renderPairQR('');});};
q('relay-reconnect').onclick=()=>action(()=>call('remote.reconnect'));
q('update-apply').onclick=()=>action(applyUpdate);
q('update-skip').onclick=()=>action(async()=>{const status=await call('system.update.status');if(status.latest)await call('system.update.skip',{version:status.latest.version});});
q('update-cancel').onclick=()=>action(()=>call('system.update.cancel'));
q('update-check').onclick=()=>action(async()=>{const status=await call('system.update.check');notify(status.state==='upToDate'?`已是最新版本 ${status.currentVersion}`:status.latest?`发现新版本 ${status.latest.version}`:'检查完成',status.state==='upToDate'?'success':'info');});
q('update-auto').onchange=(event)=>action(()=>call('system.update.settings',{autoCheck:event.target.checked}));
q('update-mirror-save').onclick=()=>action(async()=>{await call('system.update.settings',{mirrorBase:q('update-mirror').value.trim()});notify('更新镜像地址已保存','success');});
q('pair-start').onclick=()=>action(async()=>{const result=await call('remote.pair.start',{ttlSeconds:300});if(result&&result.uri){q('pair-uri').value=result.uri;await renderPairQR(result.uri);}});
q('pair-refresh').onclick=()=>action(()=>call('remote.pair.list'));
q('pair-copy').onclick=async()=>{try{await navigator.clipboard.writeText(q('pair-uri').value);notify('配对 URI 已复制','success');}catch(error){showError(new Error('复制失败，请手动复制 URI'));}};
q('pair-attempts').onclick=(event)=>{const button=event.target.closest('[data-pair-action]');if(!button)return;const id=button.dataset.pairId;const operation=button.dataset.pairAction;action(async()=>{if(operation==='approve'||operation==='reject'){if(operation==='reject'&&!confirm('确定拒绝这个移动端配对请求吗？'))return;await call('remote.pair.resolve',{pairingId:id,approved:operation==='approve'});}else if(operation==='cancel'){if(!confirm('确定取消这个待处理的配对请求吗？'))return;await call('remote.pair.cancel',{pairingId:id});}});};
q('pairings').onclick=(event)=>{const button=event.target.closest('[data-pair-action="revoke"]');if(!button)return;if(!confirm('撤销后该移动端将无法继续访问此 Desktop，确定继续吗？'))return;action(()=>call('remote.pair.revoke',{pairingId:button.dataset.pairId}));};

async function run(){try{await exchange();await refresh();openSocket().catch(showError);pollTimer=setInterval(poll,5000);}catch(error){showError(error);q('daemon').textContent='未连接';}}
run();
