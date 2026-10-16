package httpapi

import (
	"net/http"
)

func (s *Server) handleConsole(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(consoleHTML))
}

const consoleHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>X AI Operations</title>
  <style>
    :root{color-scheme:light;--ink:#172027;--muted:#637079;--line:#d9dee1;--surface:#fff;--soft:#f5f7f7;--green:#146c43;--red:#b42318;--amber:#8a4b08}
    *{box-sizing:border-box}body{margin:0;background:var(--soft);color:var(--ink);font:14px/1.45 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;letter-spacing:0}
    header{height:56px;background:#172027;color:#fff;display:flex;align-items:center;justify-content:space-between;padding:0 24px;border-bottom:3px solid #d1a21b}
    h1{font-size:17px;margin:0;font-weight:650}h2{font-size:14px;margin:0 0 12px;font-weight:650}
    .state{display:flex;gap:16px;align-items:center}.dot{display:inline-block;width:8px;height:8px;border-radius:50%;background:#4ac58b;margin-right:7px}
    nav{height:44px;background:var(--surface);border-bottom:1px solid var(--line);display:flex;align-items:end;padding:0 24px;gap:22px}
    nav button{height:44px;border:0;border-bottom:2px solid transparent;background:transparent;padding:0 2px;color:var(--muted);cursor:pointer}
    nav button.active{color:var(--ink);border-color:#25775e;font-weight:650}
    main{max-width:1440px;margin:0 auto;padding:20px 24px 40px}.toolbar{display:flex;justify-content:space-between;gap:16px;align-items:center;margin-bottom:16px}
    .summary{display:grid;grid-template-columns:repeat(5,minmax(120px,1fr));border:1px solid var(--line);background:var(--surface);margin-bottom:18px}
    .metric{padding:15px 16px;border-right:1px solid var(--line)}.metric:last-child{border-right:0}.metric span{display:block;color:var(--muted);font-size:12px}.metric strong{display:block;font-size:19px;margin-top:3px;font-weight:650}
    section{background:var(--surface);border:1px solid var(--line);margin-bottom:18px;padding:16px}.hidden{display:none}
    table{width:100%;border-collapse:collapse;table-layout:fixed}th,td{text-align:left;padding:9px 10px;border-bottom:1px solid #e8ebed;overflow-wrap:anywhere;vertical-align:top}th{font-size:12px;color:var(--muted);font-weight:600;background:#f8f9f9}tr:last-child td{border-bottom:0}
    button.command{border:1px solid #aeb8bd;background:#fff;color:var(--ink);height:32px;padding:0 12px;border-radius:4px;cursor:pointer}button.command.danger{border-color:#e7a8a3;color:var(--red)}button.command:disabled{opacity:.45;cursor:default}
    .pill{display:inline-block;padding:2px 7px;border-radius:3px;background:#e8f4ef;color:var(--green);font-size:12px}.pill.warn{background:#fff0d8;color:var(--amber)}.pill.fail{background:#fdecea;color:var(--red)}
    .empty{color:var(--muted);padding:22px 10px;text-align:center}.error{color:var(--red);white-space:pre-wrap}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
    @media(max-width:760px){header,nav{padding-left:14px;padding-right:14px;overflow-x:auto}header .state{gap:8px}#health{font-size:0;white-space:nowrap}#health .dot{margin-right:0}main{padding:14px}.summary{grid-template-columns:repeat(2,1fr)}.metric{border-bottom:1px solid var(--line)}.metric:nth-child(2n){border-right:0}.metric:last-child{border-bottom:0}.toolbar{align-items:flex-start;flex-direction:column}.table-wrap{overflow:auto}table{min-width:720px}.empty{text-align:left}}
  </style>
</head>
<body>
  <header><h1>X AI Operations</h1><div class="state"><span id="health"><i class="dot"></i>连接中</span><span id="mode" class="pill"></span></div></header>
  <nav>
    <button class="active" data-tab="overview">概览</button>
    <button data-tab="jobs">任务</button>
    <button data-tab="content">内容</button>
    <button data-tab="providers">模型</button>
    <button data-tab="alerts">告警</button>
    <button data-tab="audit">审计</button>
  </nav>
  <main>
    <div class="toolbar"><div><strong id="account">-</strong><div id="rule" style="color:var(--muted);margin-top:3px">-</div></div><div><button id="pause" class="command danger">暂停</button> <button id="resume" class="command">恢复</button></div></div>
    <div class="summary">
      <div class="metric"><span>账号状态</span><strong id="account-state">-</strong></div>
      <div class="metric"><span>待执行任务</span><strong id="pending-count">-</strong></div>
      <div class="metric"><span>已审批内容</span><strong id="approved-count">-</strong></div>
      <div class="metric"><span>开放告警</span><strong id="alert-count">-</strong></div>
      <div class="metric"><span>运行时长</span><strong id="uptime">-</strong></div>
    </div>
    <div id="global-error" class="error"></div>
    <div id="overview" class="tab">
      <section><h2>近期内容</h2><div class="table-wrap"><table><thead><tr><th style="width:15%">类型</th><th style="width:15%">状态</th><th>正文</th><th style="width:20%">创建时间</th></tr></thead><tbody id="overview-content"></tbody></table></div></section>
      <section><h2>近期审计</h2><div class="table-wrap"><table><thead><tr><th style="width:22%">事件</th><th style="width:18%">对象</th><th>详情</th><th style="width:20%">时间</th></tr></thead><tbody id="overview-audit"></tbody></table></div></section>
    </div>
    <div id="jobs" class="tab hidden"><section><h2>任务队列</h2><div class="table-wrap"><table><thead><tr><th>动作</th><th>状态</th><th>尝试</th><th>执行时间</th><th>错误</th></tr></thead><tbody id="jobs-body"></tbody></table></div></section></div>
    <div id="content" class="tab hidden"><section><h2>内容记录</h2><div class="table-wrap"><table><thead><tr><th>类型</th><th>状态</th><th style="width:45%">正文</th><th>规则版本</th><th>时间</th></tr></thead><tbody id="content-body"></tbody></table></div></section></div>
    <div id="providers" class="tab hidden"><section><h2>Provider Profiles</h2><div class="table-wrap"><table><thead><tr><th>名称</th><th>协议</th><th>模型</th><th>位置</th><th>状态</th></tr></thead><tbody id="providers-body"></tbody></table></div></section></div>
    <div id="alerts" class="tab hidden"><section><h2>系统告警</h2><div class="table-wrap"><table><thead><tr><th>级别</th><th>类型</th><th style="width:40%">消息</th><th>状态</th><th>更新时间</th></tr></thead><tbody id="alerts-body"></tbody></table></div></section></div>
    <div id="audit" class="tab hidden"><section><h2>审计日志</h2><div class="table-wrap"><table><thead><tr><th>事件</th><th>执行者</th><th>对象</th><th style="width:38%">详情</th><th>时间</th></tr></thead><tbody id="audit-body"></tbody></table></div></section></div>
  </main>
  <script>
    let csrf="",statusData=null;
    const q=s=>document.querySelector(s), qa=s=>[...document.querySelectorAll(s)];
    const esc=v=>String(v??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
    const badge=v=>'<span class="pill '+(/dead|failed|paused|rejected/.test(v)?"fail":/pending|leased|scheduled/.test(v)?"warn":"")+'">'+esc(v)+'</span>';
    const when=v=>v?new Date(v).toLocaleString():"-";
    const empty=(n,c)=>'<tr><td colspan="'+c+'" class="empty">'+n+'</td></tr>';
    async function api(path,options={}){options.headers={...(options.headers||{}),"X-CSRF-Token":csrf};const r=await fetch(path,options);const body=await r.json().catch(()=>({}));if(!r.ok)throw new Error(body.error||('HTTP '+r.status));return body}
    async function load(){
      try{
        const [status,jobsValue,contentValue,providersValue,auditValue,alertsValue]=await Promise.all([api("/api/v1/status"),api("/api/v1/jobs?limit=100"),api("/api/v1/content?limit=100"),api("/api/v1/providers"),api("/api/v1/audit?limit=100"),api("/api/v1/alerts?limit=100")]);
        const jobs=jobsValue||[],content=contentValue||[],providers=providersValue||[],audit=auditValue||[],alerts=alertsValue||[];
        statusData=status;csrf=status.csrf_token;q("#health").innerHTML='<i class="dot"></i>运行中';q("#mode").textContent=status.mode;q("#account").textContent=status.account.handle;q("#rule").textContent='规则版本 '+(status.active_rule_version||"未激活");q("#account-state").textContent=status.account.state;q("#pending-count").textContent=jobs.filter(x=>x.state==="pending"||x.state==="leased").length;q("#approved-count").textContent=content.filter(x=>x.state==="approved_auto").length;q("#uptime").textContent=formatDuration(status.uptime_seconds);
        q("#pause").disabled=status.account.state!=="active";q("#resume").disabled=status.account.state==="active";
        q("#alert-count").textContent=alerts.filter(x=>x.state==="open").length;renderContent(content);renderJobs(jobs);renderProviders(providers);renderAlerts(alerts);renderAudit(audit);q("#global-error").textContent="";
      }catch(e){q("#health").innerHTML='<i class="dot" style="background:#e24b3b"></i>异常';q("#global-error").textContent=e.message}
    }
    function renderContent(items){const rows=items.map(x=>'<tr><td>'+esc(x.kind)+'</td><td>'+badge(x.state)+'</td><td>'+esc(x.body)+'</td><td class="mono">'+esc(x.rule_version_id)+'</td><td>'+when(x.created_at)+'</td></tr>').join("");q("#content-body").innerHTML=rows||empty("暂无内容",5);q("#overview-content").innerHTML=items.slice(0,8).map(x=>'<tr><td>'+esc(x.kind)+'</td><td>'+badge(x.state)+'</td><td>'+esc(x.body)+'</td><td>'+when(x.created_at)+'</td></tr>').join("")||empty("暂无内容",4)}
    function renderJobs(items){q("#jobs-body").innerHTML=items.map(x=>'<tr><td>'+esc(x.action)+'</td><td>'+badge(x.state)+'</td><td>'+x.attempts+' / '+x.max_attempts+'</td><td>'+when(x.run_at)+'</td><td class="error">'+esc(x.last_error||"")+'</td></tr>').join("")||empty("暂无任务",5)}
    function renderProviders(items){q("#providers-body").innerHTML=items.map(x=>'<tr><td>'+esc(x.profile.name)+'</td><td>'+esc(x.profile.protocol)+'</td><td>'+esc(x.profile.model)+'</td><td>'+esc(x.profile.location)+'</td><td>'+badge(x.healthy?"configured":"unavailable")+'</td></tr>').join("")||empty("暂无 Provider",5)}
    function renderAlerts(items){q("#alerts-body").innerHTML=items.map(x=>'<tr><td>'+badge(x.severity)+'</td><td>'+esc(x.kind)+'</td><td>'+esc(x.message)+'</td><td>'+badge(x.state)+'</td><td>'+when(x.updated_at)+'</td></tr>').join("")||empty("暂无告警",5)}
    function renderAudit(items){const full=items.map(x=>'<tr><td>'+esc(x.event_type)+'</td><td>'+esc(x.actor_type)+' / '+esc(x.actor_id)+'</td><td>'+esc(x.subject_type)+' / '+esc(x.subject_id)+'</td><td class="mono">'+esc(x.details_json)+'</td><td>'+when(x.created_at)+'</td></tr>').join("");q("#audit-body").innerHTML=full||empty("暂无审计记录",5);q("#overview-audit").innerHTML=items.slice(0,8).map(x=>'<tr><td>'+esc(x.event_type)+'</td><td>'+esc(x.subject_type)+' / '+esc(x.subject_id)+'</td><td class="mono">'+esc(x.details_json)+'</td><td>'+when(x.created_at)+'</td></tr>').join("")||empty("暂无审计记录",4)}
    function formatDuration(s){const h=Math.floor(s/3600),m=Math.floor((s%3600)/60);return h+"h "+m+"m"}
    qa("nav button").forEach(b=>b.onclick=()=>{qa("nav button").forEach(x=>x.classList.toggle("active",x===b));qa(".tab").forEach(x=>x.classList.toggle("hidden",x.id!==b.dataset.tab))});
    q("#pause").onclick=async()=>{await api("/api/v1/accounts/"+statusData.account.id+"/pause",{method:"POST",headers:{"Content-Type":"application/json"},body:'{"reason":"operator requested"}'});await load()};
    q("#resume").onclick=async()=>{await api("/api/v1/accounts/"+statusData.account.id+"/resume",{method:"POST"});await load()};
    load();setInterval(load,15000);
  </script>
</body>
</html>`
