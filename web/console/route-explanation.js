import {buildRouteViewModel,previewOf,previewLabel,previewPlaceholder,sourceRequestId} from './observability-model.js';

const svgNS='http://www.w3.org/2000/svg';
const glyphs={
  laptop:'<rect x="4" y="3" width="24" height="18" rx="2"/><path d="M2 24h28l-3 4H5z"/><path d="m10 9 4 3-4 3m7 0h5"/>',
  plane:'<path d="M3 14 28 3 18 29l-4-11-11-4Z"/><path d="M14 18 28 3"/>',
  response:'<path d="M5 4h23v18H15l-8 7v-7H5z"/><path d="M10 10h13M10 15h9"/>',
  error:'<path d="m16 3 14 25H2L16 3Z"/><path d="M16 11v8m0 4h.01"/>',
  provider:'<rect x="4" y="3" width="24" height="10" rx="2"/><rect x="4" y="19" width="24" height="10" rx="2"/><path d="M9 8h.1M9 24h.1M15 8h9M15 24h9"/>',
};
const create=(tag,className='',text)=>{const node=document.createElement(tag);if(className)node.className=className;if(text!==undefined)node.textContent=String(text);return node;};
const svgIcon=name=>{const icon=document.createElementNS(svgNS,'svg');icon.setAttribute('viewBox','0 0 32 32');icon.setAttribute('aria-hidden','true');icon.innerHTML=glyphs[name]||glyphs.provider;return icon;};
const money=value=>value==null?'—':new Intl.NumberFormat('en-US',{style:'currency',currency:'USD',maximumFractionDigits:4}).format(Number(value));
const compact=value=>value==null?'—':new Intl.NumberFormat('en',{notation:'compact',maximumFractionDigits:1}).format(Number(value));

export function mountRouteExplanation(container,initialDetail={},initialOptions={}) {
  let detail=initialDetail,options=initialOptions,view,disposed=false,playing=false,timer=null,index=0,followLatest=true,visible=true;
  let inventoryKey='',sourceKey='',conditionKey='',filterKey='',inspected='',filter='all',speed=1,motion=true;
  const choiceRefs=new Map();
  const motionQuery=globalThis.matchMedia?.('(prefers-reduced-motion: reduce)');
  let reduced=Boolean(motionQuery?.matches);
  const root=create('div','route-explanation');container.append(root);
  const t=(en,zh)=>(options.locale||'en').startsWith('zh')?zh:en;
  const header=create('div','route-explanation-head'),title=create('div'),heading=create('h3'),subtitle=create('p','cell-sub');title.append(heading,subtitle);
  const controls=create('div','route-playback-controls');
  const button=(label,action)=>{const el=create('button','btn small',label);el.type='button';el.addEventListener('click',action);controls.append(el);return el;};
  const play=button('▶',()=>{followLatest=false;playing=!playing;if(index>=view.events.length-1)index=0;paint();schedule();});
  const step=button('→',()=>{followLatest=false;playing=false;index=Math.min(view.events.length-1,index+1);paint();stopTimer();});
  const replay=button('↺',()=>{followLatest=false;index=0;playing=!reduced&&motion;paint();schedule();});
  const latest=button('',()=>{followLatest=true;playing=false;index=Math.max(0,view.events.length-1);stopTimer();paint();});
  const rate=create('select','select route-speed');rate.setAttribute('aria-label','Playback speed');
  for(const n of [.5,1,2,3]){const option=create('option','',`${n}×`);option.value=String(n);option.selected=n===1;rate.append(option)}
  rate.addEventListener('change',()=>{speed=Number(rate.value)||1;paint();schedule()});controls.append(rate);
  const motionButton=button('',()=>{motion=!motion;if(!motion){playing=false;stopTimer()}paint();try{localStorage.setItem('smartapi-observability-motion',String(motion))}catch{}});
  header.append(title,controls);
  const stage=create('div','route-stage'),links=document.createElementNS(svgNS,'svg');links.classList.add('route-links');links.setAttribute('aria-hidden','true');
  const packets=create('div','route-packets'),packet=create('span','route-packet');packet.append(svgIcon('plane'));packets.append(packet);
  const sourceColumn=create('div','route-sources'),gatewayColumn=create('div','route-gateway-column'),providerColumn=create('div','route-providers');
  const gateway=create('div','route-gateway'),brand=create('img');brand.src=new URL('./brand.svg',document.baseURI).href;brand.alt='';
  const gatewayName=create('strong','','SmartAPI'),gatewayState=create('span','cell-sub');gateway.append(brand,gatewayName,gatewayState);gatewayColumn.append(gateway);
  stage.append(links,packets,sourceColumn,gatewayColumn,providerColumn);
  const status=create('div','route-live-status');status.setAttribute('role','status');status.setAttribute('aria-live','polite');
  const statusTitle=create('strong'),statusText=create('span','cell-sub');status.append(statusTitle,statusText);
  const conditions=create('details','route-conditions'),conditionsSummary=create('summary'),conditionBody=create('div','route-conditions-body');
  const filterBar=create('div','route-candidate-filter'),filterLabel=create('label'),filterSelect=create('select','select');filterLabel.append(filterSelect);filterBar.append(filterLabel);
  filterSelect.setAttribute('aria-label','Candidate condition filter');
  filterSelect.addEventListener('change',()=>{filter=filterSelect.value;conditionKey='';paintConditions()});
  const candidateChoices=create('div','route-candidate-choices'),conditionContent=create('div');conditionBody.append(filterBar,candidateChoices,conditionContent);conditions.append(conditionsSummary,conditionBody);
  root.append(header,stage,status,conditions);
  const providerRefs=new Map(),sourceRefs=new Map();
  const cleanup=new AbortController();
  try{motion=localStorage.getItem('smartapi-observability-motion')!=='false'}catch{}

  function stopTimer(){if(timer!==null){clearTimeout(timer);timer=null}}
  function canMove(){return motion&&!reduced&&visible&&!document.hidden&&!disposed}
  function schedule(){
    stopTimer();if(!playing||!canMove()||followLatest)return;
    if(index>=view.events.length-1){playing=false;paintControls();return}
    const current=Date.parse(view.events[index]?.at||''),next=Date.parse(view.events[index+1]?.at||'');
    const delay=Math.max(260,Math.min(1400,Number.isFinite(next-current)?(next-current)/speed:600/speed));
    timer=setTimeout(()=>{timer=null;index++;paint();schedule()},delay);
  }

  function providerImage(node){
    const value=`${node.provider_id} ${node.provider_name}`.toLowerCase();
    const file=value.includes('codex')?'codex-color.svg':value.includes('anthropic')||value.includes('claude')?'anthropic.svg':value.includes('openai')?'openai.svg':null;
    if(!file)return svgIcon('provider');
    const image=create('img',file==='codex-color.svg'?'':'monochrome');image.src=new URL(`./observability-icons/${file}`,document.baseURI).href;image.alt='';return image;
  }

  function drawInventory(){
    const key=JSON.stringify([options.locale,view.providers.map(p=>[p.key,p.provider_name,p.state,p.candidates.map(c=>[c.candidate_id,c.connection_id,c.connection_name,c.eligible,c.exclusion_reason])])]);
    if(key===inventoryKey)return;
    inventoryKey=key;providerColumn.replaceChildren();providerRefs.clear();
    for(const provider of view.providers){
      const card=create('div','route-provider'),head=create('div','route-provider-head'),logo=create('span','route-provider-logo');logo.append(providerImage(provider));
      const name=create('strong','',provider.provider_name),stepLabel=create('small','',`${t('Step','步骤')} ${provider.route_step||'—'}`),stateLabel=create('span','route-node-state');
      const caption=create('div');caption.append(name,stepLabel);head.append(logo,caption,stateLabel);card.append(head);
      const connections=create('div','route-connections'),candidateRefs=new Map();
      for(const candidate of provider.candidates){
        const id=candidate.candidate_id||`${candidate.route_step}:${candidate.connection_id}`;
        const node=create('button','route-connection');node.type='button';
        const text=create('span','',candidate.connection_name||candidate.connection_id||t('Historical connection','历史连接')),state=create('small');node.append(text,state);
        node.addEventListener('click',()=>{inspected=id;conditions.open=true;conditionKey='';paintConditions()});
        connections.append(node);candidateRefs.set(id,{node,state,candidate});
      }
      if(!provider.candidates.length)connections.append(create('span','cell-sub',t('No matching connections','没有匹配的连接')));
      card.append(connections);providerColumn.append(card);providerRefs.set(provider.key,{card,stateLabel,candidateRefs,provider});
    }
  }

  function sources(){
    if(options.sources?.length)return options.sources;
    const request=detail.raw||detail.client_request||detail;
    return [{source_id:`request:${view.requestId}`,source_kind:'request',title:request.client_kind||detail.clientKind||t('API client','API 客户端'),selected_request_id:view.requestId,session_id:view.sessionId,
      caller:request.caller||detail.apiKey||'',content_preview:previewOf(request),request_count:1,selected_request_status:request.status,
      usage:request.usage,usage_state:request.usage_state||'unknown',cost_state:request.cost_state||'unknown',known_cost:request.cost_state==='unknown'?null:request.total_cost}];
  }

  function drawSources(){
    const rows=sources().slice(0,4),key=JSON.stringify([options.locale,rows.map(s=>[s.source_id,s.title,s.caller,s.content_preview,s.request_count,s.known_cost,s.cost_state,s.usage_state,s.selected_request_status,options.sessionHref?.(s.session_id),options.requestHref?.(sourceRequestId(s))])]);
    if(key===sourceKey){for(const [id,ref] of sourceRefs){const selected=id===(options.selectedSourceId||rows[0]?.source_id);ref.card.classList.toggle('selected',selected);ref.select.setAttribute('aria-pressed',String(selected))}return}
    sourceKey=key;sourceColumn.replaceChildren();sourceRefs.clear();
    for(const source of rows){
      const card=create('div','route-source-card'),select=create('button','route-source-select');select.type='button';
      const top=create('div','route-source-heading'),laptop=create('span','route-client-icon');laptop.append(svgIcon('laptop'));
      const stateLabels={waiting:['Waiting','等待中'],active:['Calling','调用中'],streaming:['Streaming','流式输出'],recovering:['Recovering','恢复中'],success:['Success','成功'],failed:['Failed','失败'],canceled:['Canceled','已取消'],received:['Received','已接收'],delivering:['Delivering','交付中']};
      const name=create('strong','',source.title||source.session_id||sourceRequestId(source)),statusLabel=create('small','route-source-state',stateLabels[source.selected_request_status]?t(...stateLabels[source.selected_request_status]):source.selected_request_status||'');
      name.setAttribute('data-i18n-skip','');top.append(laptop,name,statusLabel);
      const client=create('small','cell-sub',[source.client_kind,source.caller].filter(Boolean).join(' · '));client.setAttribute('data-i18n-skip','');
      const p=source.content_preview||previewOf(source.selected_request||{}),preview=create('span','route-source-preview');
      const prefix=create('b','',`${previewLabel(p,options.locale)} `),copy=create('span','',p.preview_text||previewPlaceholder(p,options.locale));copy.setAttribute('data-i18n-skip','');preview.append(prefix,copy);
      const usage=source.usage_state==='unknown'?`— ${t('tokens','Token')}`:`${compact(Number(source.usage?.input_tokens||0)+Number(source.usage?.output_tokens||0))} ${t('tokens','Token')}`;
      const metrics=create('small','route-source-metrics',`${source.request_count||1} ${t('requests','次请求')} · ${usage} · ${source.cost_state==='partial'?'≈ ':''}${money(source.known_cost)}`);
      select.append(top,client,preview,metrics);select.addEventListener('click',()=>options.onSelectSource?.(source));
      card.append(select);
      const actions=create('div','route-source-links');
      if(source.session_id){const link=create('a','','Session ↗');link.href=options.sessionHref?.(source.session_id)||`#/sessions/${encodeURIComponent(source.session_id)}`;actions.append(link)}
      const id=sourceRequestId(source);if(id){const link=create('a','','Request ↗');link.href=options.requestHref?.(id)||`#/requests/${encodeURIComponent(id)}`;actions.append(link)}
      card.append(actions);card.classList.toggle('selected',source.source_id===(options.selectedSourceId||rows[0]?.source_id));
      select.setAttribute('aria-pressed',String(card.classList.contains('selected')));sourceColumn.append(card);sourceRefs.set(source.source_id,{card,select});
    }
  }

  function paintControls(){
    play.textContent=playing?'Ⅱ':`▶ ${t('Play','播放')}`;play.setAttribute('aria-label',playing?t('Pause replay','暂停回放'):t('Play replay','播放回放'));
    play.disabled=reduced||!motion||view.events.length<2;step.disabled=index>=view.events.length-1&&!followLatest;
    step.textContent=t('Step →','单步 →');replay.textContent=t('Replay ↺','重播 ↺');latest.textContent=view.live?t('Live','实时'):t('Latest','最新状态');
    latest.classList.toggle('primary',followLatest);motionButton.textContent=motion&&!reduced?t('Motion on','动效开'):t('Motion off','动效关');
    motionButton.disabled=reduced;motionButton.setAttribute('aria-pressed',String(motion&&!reduced));
    rate.setAttribute('aria-label',t('Playback speed','播放速度'));
    root.classList.toggle('no-motion',!canMove()||(!playing&&!followLatest)||(!view.live&&followLatest));
  }

  function paintStatus(){
    const labels={received:['Request received','已接收请求'],planning:['Evaluating route candidates','正在评估路由候选'],waiting:['Waiting · no upstream call','等待准入 · 尚未调用上游'],admitted:['Admitted · preparing dispatch','已准入 · 准备调用'],calling:['Calling the selected connection','正在调用高亮连接'],
      'upstream-response':['Upstream event received','已收到上游事件'],'upstream-failed':['Upstream failure returned to SmartAPI','上游失败已返回 SmartAPI'],'admission-failed':['Admission failed · no upstream call','准入失败 · 尚未调用上游'],recovering:['Recovering through the next connection','正在转向下一条连接'],delivering:['Delivering visible response','正在交付可见响应'],processed:['Processing completed','处理结束'],complete:['Client delivery completed','客户端交付完成'],failed:['Request failed','请求失败'],canceled:['Request stopped','请求已停止']};
    const label=labels[view.phase]||labels.received;
    statusTitle.textContent=t(...label);gatewayState.textContent=t(...label);
    const current=followLatest?view.events.length:Math.min(index+1,view.events.length),stamp=view.lastEvent?.at?new Date(view.lastEvent.at).toLocaleTimeString(options.locale||'en'):'—';
    statusText.textContent=`${followLatest?(view.live?t('Live facts','实时记录'):t('Recorded facts','已记录事实')):t('Replay','历史回放')} · ${current}/${view.events.length} · ${stamp}${view.eventsTruncated?` · ${view.eventsTruncated} ${t('events omitted','条步骤已省略')}`:''}`;
    heading.textContent=options.title||t('Request explanation','请求解释');
    subtitle.textContent=`${view.providers.length} ${t('route steps','个路由步骤')} · ${view.candidateCount} ${t('candidate connections','条候选连接')}${view.coverage!=='complete'?` · ${t('Historical coverage is incomplete','历史记录不完整')}`:''}`;
  }

  function paintNodes(){
    for(const provider of view.providers){
      const ref=providerRefs.get(provider.key);if(!ref)continue;
      for(const cls of ['active','selected','failed','attempted'])ref.card.classList.toggle(cls,Boolean(provider[cls]));
      ref.stateLabel.textContent=provider.active?t('Active','激活'):provider.selected?t('Selected','已选'):provider.state==='no_connections'?t('No connections','无连接'):provider.candidates.some(c=>c.eligible)?t('Candidate','候选'):t('Excluded','已排除');
      for(const candidate of provider.candidates){const node=ref.candidateRefs.get(candidate.candidate_id||`${candidate.route_step}:${candidate.connection_id}`);if(!node)continue;
        for(const cls of ['active','selected','failed','attempted'])node.node.classList.toggle(cls,Boolean(candidate[cls]));
        node.node.classList.toggle('excluded',!candidate.eligible);
        node.state.textContent=candidate.active?t('Active','激活'):candidate.failed?t('Failed','失败'):candidate.selected?t('Selected','已选'):!candidate.eligible?t('Excluded','排除'):t('Eligible','可选');
      }
    }
    gateway.classList.toggle('recovering',['upstream-failed','recovering'].includes(view.phase));
  }

  function matches(candidate){
    if(filter==='all')return true;if(filter==='eligible')return candidate.eligible;if(filter==='excluded')return !candidate.eligible;
    if(filter==='active')return candidate.active;if(filter==='failed')return candidate.failed;
    return (candidate.conditions||[]).some(c=>c.condition_code===filter);
  }

  function paintConditions(){
    const all=view.providers.flatMap(p=>p.candidates),key=JSON.stringify([all,filter,inspected,options.locale]);if(key===conditionKey)return;conditionKey=key;
    const matching=all.filter(matches);
    const selected=matching.find(c=>c.candidate_id===inspected)||matching.find(c=>c.selected)||matching[0];
    conditionsSummary.textContent=`${t('Candidates and conditions','候选与条件')} · ${all.filter(matches).length}/${all.length}`;
    const labels={all:t('All candidates','全部候选'),eligible:t('Eligible','可选'),excluded:t('Excluded','已排除'),active:t('Active','激活'),failed:t('Attempt failed','尝试失败')};
    const codes=[...new Set(all.flatMap(c=>(c.conditions||[]).map(x=>x.condition_code)))];
    filterSelect.setAttribute('aria-label',t('Candidate condition filter','候选条件筛选'));
    const nextFilterKey=JSON.stringify([labels,codes]);
    if(nextFilterKey!==filterKey){filterKey=nextFilterKey;filterSelect.replaceChildren();for(const code of [...Object.keys(labels),...codes]){const el=create('option','',labels[code]||code);el.value=code;filterSelect.append(el)}}
    filterSelect.value=filter;
    const currentIds=new Set(all.map(c=>c.candidate_id));
    for(const [id,chip]of choiceRefs)if(!currentIds.has(id)){chip.remove();choiceRefs.delete(id)}
    for(const candidate of all){
      let chip=choiceRefs.get(candidate.candidate_id);
      if(!chip){chip=create('button','btn small');chip.type='button';chip.addEventListener('click',()=>{inspected=candidate.candidate_id;conditionKey='';paintConditions()});candidateChoices.append(chip);choiceRefs.set(candidate.candidate_id,chip)}
      chip.textContent=`${candidate.provider_name||candidate.provider_id} · ${candidate.connection_name||candidate.connection_id}`;
      chip.classList.toggle('primary',candidate.candidate_id===selected?.candidate_id);chip.hidden=!matches(candidate);
    }
    conditionContent.replaceChildren();
    if(!all.length){conditionContent.append(create('p','cell-sub',t('Complete candidate snapshots were not recorded for this request.','此请求没有完整的候选快照。')));return}
    if(!all.some(matches)){conditionContent.append(create('p','cell-sub',t('No candidates match this filter. All route nodes remain visible.','没有符合此筛选的候选，图中仍保留全部路由节点。')));return}
    if(!selected)return;
    const text=create('p','cell-sub',[selected.upstream_model,selected.protocol,selected.exclusion_reason].filter(Boolean).join(' · '));text.setAttribute('data-i18n-skip','');conditionContent.append(text);
    const list=create('dl','route-condition-list');
    const conditionLabels={provider_enabled:['Provider enabled','渠道启用'],connection_enabled:['Connection enabled','连接启用'],credential_available:['Credential available','凭据可用'],authentication:['Authentication','身份认证'],reachability:['Reachability','连通性'],connection_cooldown:['Connection cooldown','连接冷却'],quota:['Quota','配额'],capabilities:['Capabilities','能力匹配'],circuit_breaker:['Circuit breaker','熔断'],model_cooldown:['Model cooldown','模型冷却'],protocol_compatibility:['Protocol compatibility','协议兼容']};
    const results={pass:t('Pass','通过'),fail:t('Excluded','排除'),unknown:t('Unknown','未知'),not_evaluated:t('Not evaluated','未评估')};
    for(const condition of selected.conditions||[]){const row=create('div',`condition-${condition.result}`),name=create('dt','',conditionLabels[condition.condition_code]?t(...conditionLabels[condition.condition_code]):condition.condition_code),value=create('dd','',results[condition.result]||condition.result);row.append(name,value);if(condition.reason)row.append(create('small','cell-sub',condition.reason));list.append(row)}
    if(!(selected.conditions||[]).length)list.append(create('p','cell-sub',t('Condition evidence is unavailable.','没有记录条件证据。')));
    conditionContent.append(list,create('small','cell-sub',t('Conditions describe the routing decision at the time of this request.','条件反映此请求选路时的判断。')));
    const updates=view.visibleEvents.filter(event=>['ADMISSION_FAILED','ATTEMPT_FAILED','CONNECTION_REBOUND','ROUTE_FALLBACK','PROVIDER_EXHAUSTED'].includes(event.type)&&event.provider_id===selected.provider_id&&event.connection_id===selected.connection_id&&(!event.route_step||event.route_step===selected.route_step));
    if(updates.length){
      conditionContent.append(create('h4','',t('Execution decision updates','执行时的决策更新')));
      const history=create('ol','route-decision-updates');
      for(const event of updates){const entry=create('li','',`${event.seq?`#${event.seq} `:''}${event.type}${event.failure_type?' · '+event.failure_type:''}${event.message?' · '+event.message:''}`);entry.setAttribute('data-i18n-skip','');history.append(entry)}
      conditionContent.append(history);
    }
  }

  function position(){
    if(disposed||!stage.isConnected)return;
    const bounds=stage.getBoundingClientRect(),gate=gateway.getBoundingClientRect();
    links.setAttribute('viewBox',`0 0 ${bounds.width} ${bounds.height}`);links.replaceChildren();
    const vertical=getComputedStyle(stage).gridTemplateColumns.trim().split(/\s+/).length===1;
    const point=(r,side)=>side==='bottom'||side==='top'?{x:r.left+r.width/2-bounds.left,y:(side==='bottom'?r.bottom:r.top)-bounds.top}:{x:(side==='right'?r.right:r.left)-bounds.left,y:r.top+r.height/2-bounds.top};
    const curve=(a,b)=>{if(vertical){const dy=Math.max(16,Math.abs(b.y-a.y)*.5);return `M ${a.x} ${a.y} C ${a.x} ${a.y+dy}, ${b.x} ${b.y-dy}, ${b.x} ${b.y}`}const dx=Math.max(25,Math.abs(b.x-a.x)*.48);return `M ${a.x} ${a.y} C ${a.x+dx} ${a.y}, ${b.x-dx} ${b.y}, ${b.x} ${b.y}`};
    const reverse=(a,b)=>{if(vertical){const dy=Math.max(16,Math.abs(b.y-a.y)*.5);return `M ${b.x} ${b.y} C ${b.x} ${b.y-dy}, ${a.x} ${a.y+dy}, ${a.x} ${a.y}`}const dx=Math.max(25,Math.abs(b.x-a.x)*.48);return `M ${b.x} ${b.y} C ${b.x-dx} ${b.y}, ${a.x+dx} ${a.y}, ${a.x} ${a.y}`};
    let sourcePath='',sourceReturn='',providerPath='',providerReturn='';
    const chosenSource=options.selectedSourceId||sources()[0]?.source_id;
    for(const [id,ref] of sourceRefs){const a=point(ref.card.getBoundingClientRect(),vertical?'bottom':'right'),b=point(gate,vertical?'top':'left'),d=curve(a,b),path=document.createElementNS(svgNS,'path');path.setAttribute('d',d);path.classList.add('route-edge');if(id===chosenSource){path.classList.add('selected');sourcePath=d;sourceReturn=reverse(a,b)}links.append(path)}
    for(const provider of view.providers){const ref=providerRefs.get(provider.key);if(!ref)continue;const a=point(gate,vertical?'bottom':'right'),b=point(ref.card.getBoundingClientRect(),vertical?'top':'left'),d=curve(a,b),path=document.createElementNS(svgNS,'path');path.setAttribute('d',d);path.classList.add('route-edge');if(provider.selected){path.classList.add(view.phase==='upstream-failed'?'failed':'selected');providerPath=d;providerReturn=reverse(a,b)}links.append(path)}
    const path=view.packet==='client-request'?sourcePath:view.packet==='upstream-request'?providerPath:['upstream-response','upstream-error'].includes(view.packet)?providerReturn:sourceReturn;
    packet.hidden=!view.packet||!path;packet.className=`route-packet ${String(view.packet||'').includes('error')?'error':''}`;
    packet.replaceChildren(svgIcon(String(view.packet||'').includes('error')?'error':String(view.packet||'').includes('response')?'response':'plane'));
    if(path){packet.style.offsetPath=`path('${path}')`;packet.style.animationDuration=`${1.35/speed}s`;packet.style.animationPlayState=canMove()&&(playing||(followLatest&&view.live))?'running':'paused';}
  }

  function paint(){
    const logo=new URL(document.documentElement.dataset.theme==='dark'?'./brand-reverse.svg':'./brand.svg',document.baseURI).href;
    if(brand.src!==logo)brand.src=logo;
    const events=buildRouteViewModel(detail).events;
    const seq=followLatest?Infinity:events[index]?.seq||index+1;
    view=buildRouteViewModel(detail,seq);root.classList.toggle('compact',options.mode==='compact');
    drawInventory();drawSources();paintControls();paintNodes();paintStatus();paintConditions();requestAnimationFrame(position);
  }
  const resize=typeof ResizeObserver!=='undefined'?new ResizeObserver(()=>requestAnimationFrame(position)):null;resize?.observe(stage);
  providerColumn.addEventListener('scroll',()=>requestAnimationFrame(position),{signal:cleanup.signal,passive:true});
  const intersection=typeof IntersectionObserver!=='undefined'?new IntersectionObserver(entries=>{visible=entries[0]?.isIntersecting!==false;paintControls();if(visible)schedule();else stopTimer();}):null;intersection?.observe(root);
  document.addEventListener('visibilitychange',()=>{paintControls();if(document.hidden)stopTimer();else schedule()},{signal:cleanup.signal});
  const motionChange=event=>{reduced=event.matches;if(reduced){playing=false;stopTimer()}paint()};motionQuery?.addEventListener?.('change',motionChange);
  function update(nextDetail=detail,nextOptions=options){
    const previousId=view?.requestId;detail=nextDetail;options=nextOptions;
    const full=buildRouteViewModel(detail);
    if(previousId!==full.requestId){
      stopTimer();followLatest=full.live;playing=!full.live&&motion&&!reduced&&options.autoPlay!==false;index=playing?0:Math.max(0,full.events.length-1);
      inspected='';inventoryKey='';sourceKey='';conditionKey='';conditions.open=options.mode==='detail';
    }
    paint();schedule();
  }
  update(initialDetail,initialOptions);
  return {container,update,relayout:()=>requestAnimationFrame(position),pause:()=>{playing=false;stopTimer();paintControls()},destroy(){disposed=true;stopTimer();resize?.disconnect();intersection?.disconnect();cleanup.abort();motionQuery?.removeEventListener?.('change',motionChange);root.remove()}};
}
