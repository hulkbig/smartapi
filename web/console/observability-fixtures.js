// Explicit synthetic observations for the standalone Mock adapter only. The
// HTTP adapter never imports these records as a fallback for real API errors.
const copy=value=>JSON.parse(JSON.stringify(value));
const live=new Set(['received','waiting','active','recovering','streaming','delivering']);
const totalCost=r=>Number(r.total_cost||0);
const coverage=(rows,key)=>rows.length&&rows.every(r=>r[key]==='known')?'known':rows.some(r=>['known','partial'].includes(r[key]))?'partial':'unknown';
const sum=(rows,fn)=>rows.reduce((n,row)=>n+Number(fn(row)||0),0);
const use=(rows,key)=>sum(rows,row=>row.usage?.[key]);

function selectPreview(row,previous) {
  const p=row.content_preview;
  p.preview_role=p.request_excerpt?'request':p.response_excerpt?'response':'';
  p.preview_text=p.request_excerpt||p.response_excerpt||'';
  if(previous && p.request_excerpt && p.request_excerpt===previous.content_preview.request_excerpt && p.response_excerpt) {
    p.preview_role='response';p.preview_text=p.response_excerpt;p.preview_reason='repeated_input';
  }
  if(previous && p.request_excerpt===previous.content_preview.request_excerpt && !p.response_excerpt)p.preview_reason='repeated_waiting';
}

export function createObservationFixtures(data) {
  const now=Date.now(),records=[],details=new Map(),legacy=new Map(data.requests.map(r=>[r.id,r]));
  const providers=data.providers.filter(p=>p.id!=='azure-openai').slice(0,4);
  const providerID=value=>data.providers.find(p=>p.id===value||p.name===value)?.id || value;
  const sessions=new Map();
  const sessionFor=(caller,conversation)=>{
    if(!conversation||conversation==='—')return null;
    const key=`${caller}\0${conversation}`;
    if(!sessions.has(key))sessions.set(key,`ses_demo_${sessions.size+1}`);
    return sessions.get(key);
  };
  const makeRaw=(r,index,overrides={})=>{
    const caller=data.apiKeys.find(key=>key.name===r.apiKey)?.id || `key_demo_${String(r.apiKey||'internal-test').replaceAll(/[^\w-]/g,'_')}`;
    const raw={...(r.raw||{}),id:r.id,timestamp:new Date(now-(index+1)*22000).toISOString(),caller:r.apiKey||'internal-test',caller_identity_id:caller,
      logical_model_id:r.model,provider_id:providerID(r.providerId),upstream_model:r.upstreamModel||r.model,protocol:r.protocol||'openai-chat',stream:Boolean(r.stream),
      status:r.canceled?'canceled':r.failed?'failed':'success',outcome:r.canceled||r.failed?'FAILED':r.recovered?'RECOVERED_SUCCESS':'DIRECT_SUCCESS',
      session_id:sessionFor(caller,r.conversation),conversation_id:r.conversation==='—'?null:r.conversation,client_kind:r.apiKey==='coding-agent-key'?'codex':'api',
      usage:{input_tokens:r.inputTokens||0,output_tokens:r.outputTokens||0,cache_read_tokens:r.cacheReadTokens||0,cache_write_tokens:r.cacheWriteTokens||0,reasoning_tokens:r.reasoningTokens||0},
      usage_state:r.usageObserved?'known':'unknown',cost_state:r.raw?.cost_observed?'known':'unknown',total_cost:r.raw?.total_cost||0,attempts:r.attempts||1,
      content_preview:{request_excerpt:r.apiKey==='coding-agent-key'?'Review the routing changes and explain the result.':`Inspect ${r.model} and summarize the response.`,response_excerpt:`${r.recovered?'Recovered and completed':'Completed'} ${r.id}.`,content_state:'recorded',truncated:false},...overrides};
    raw.observation={schema_version:1,association_state:raw.session_id?'linked':'unlinked',association_source:'fixture',trace_revision:1,last_event_seq:1,usage_state:raw.usage_state,cost_state:raw.cost_state};
    return raw;
  };
  data.requests.forEach((r,index)=>records.push(makeRaw(r,index)));
  // Separate fixtures exercise waiting, streaming, independent requests and a
  // second page of sources; they do not alter old request outcomes.
  const extras=[
    {id:'req_demo_waiting',model:'smart',apiKey:'coding-agent-key',providerId:'codex-personal',conversation:data.requests[0]?.conversation,stream:true,status:'waiting',session_id:records[0]?.session_id},
    {id:'req_demo_streaming',model:'claude-opus',apiKey:'internal-test',providerId:'anthropic-main',conversation:'conv_demo_stream',stream:true,status:'streaming'},
    {id:'req_demo_independent',model:'smart',apiKey:'internal-test',providerId:'openrouter-main',conversation:null,stream:false,status:'failed',session_id:null},
  ];
  for(const [i,extra] of extras.entries()) {
    const raw=makeRaw(extra,i,{status:extra.status,outcome:live.has(extra.status)?'':'FAILED',...(Object.hasOwn(extra,'session_id')?{session_id:extra.session_id}:{}),
      usage_state:'unknown',cost_state:'unknown',usage:{input_tokens:0,output_tokens:0},
      content_preview:{request_excerpt:i===0?'Review the routing changes and explain the result.':i===1?'Explain how the selected Provider handles this request.':'Check model availability.',response_excerpt:i===1?'The request is streaming through the selected connection.':'',content_state:i===1?'partial':'recorded',truncated:false}});
    raw.observation.usage_state=raw.usage_state;raw.observation.cost_state=raw.cost_state;
    records.push(raw);
    const item={...extra,time:new Date(raw.timestamp).toLocaleTimeString('en-GB'),timestamp:raw.timestamp,logicalModel:raw.logical_model_id,
      result:extra.status==='failed'?'Failed':extra.status==='waiting'?'Waiting':'Streaming',live:live.has(extra.status),failed:extra.status==='failed',recovered:false,
      route:extra.providerId,attempts:extra.status==='waiting'?0:1,latency:'—',totalDuration:'—',elapsedDuration:'2.1s',ttft:'—',tokens:'—',streamMode:extra.stream?'Streaming':'Non-streaming',raw};
    data.requests.push(item);legacy.set(item.id,item);
  }
  for(const raw of records) {
    const primary=providers.find(p=>p.id===raw.provider_id)||providers[0];
    const sequence=raw.outcome==='RECOVERED_SUCCESS' && raw.attempts>2?['codex-personal','openrouter-main',raw.provider_id]:raw.outcome==='RECOVERED_SUCCESS'?[raw.provider_id,raw.provider_id]:[raw.provider_id];
    raw._participating=raw.status==='waiting'?[]:[...new Set(sequence)];raw._pending=raw.status==='waiting'?[raw.provider_id]:[];
    const routePrimary=providers.find(p=>p.id===sequence[0])||primary;
    const routeProviders=[routePrimary,...providers.filter(p=>p.id!==routePrimary.id)];
    const steps=routeProviders.map((p,index)=>({route_step:index+1,provider_id:p.id,provider_name:p.name,upstream_model:raw.upstream_model||raw.logical_model_id,candidate_count:p.id===primary.id?2:1,state:index===3?'excluded':'eligible'}));
    const candidates=steps.flatMap((step,index)=>Array.from({length:step.candidate_count},(_,connectionIndex)=>({
      candidate_id:`${raw.id}:candidate:${index}:${connectionIndex}`,route_step:step.route_step,provider_id:step.provider_id,provider_name:step.provider_name,
      connection_id:`${step.provider_id}-connection-${connectionIndex+1}`,connection_name:`Account ${String.fromCharCode(65+connectionIndex)}`,upstream_model:step.upstream_model,protocol:raw.protocol,
      eligible:index!==3,exclusion_reason:index===3?'This fixture route requires a different protocol.':'',exclusion_code:index===3?'protocol_compatibility':'',rank:connectionIndex+1,
      conditions:[{condition_code:'connection_enabled',result:'pass'},{condition_code:'quota',result:index===2?'unknown':'pass'},{condition_code:'protocol_compatibility',result:index===3?'fail':'pass'}],
    })));
    const events=[];const add=(type,provider='',connection=1)=>{const step=steps.find(s=>s.provider_id===provider);events.push({seq:events.length+1,type,at:new Date(Date.parse(raw.timestamp)+events.length*85).toISOString(),
      ...(provider?{provider_id:provider,connection_id:`${provider}-connection-${connection}`,route_step:step?.route_step||1}:{}),...(['ATTEMPT_FAILED'].includes(type)?{failure_type:'rate_limit',message:'Fixture: upstream rate limit'}:{})})};
    add('REQUEST_RECEIVED');add('MODEL_RESOLVED');add('ROUTE_RESOLVED');
    sequence.forEach((p,index)=>{
      const connection=sequence.length===2&&index===1?2:1;
      add('CONNECTION_SELECTED',p,connection);add('ATTEMPT_STARTED',p,connection);
      if(raw.status==='waiting')return;
      add('ADMISSION_COMPLETED',p,connection);add('UPSTREAM_DISPATCHED',p,connection);
      if(index<sequence.length-1){add('ATTEMPT_FAILED',p,connection);add(index&&p!==sequence[index+1]?'ROUTE_FALLBACK':'CONNECTION_REBOUND',sequence[index+1],sequence.length===2?2:1)}
      else if(raw.status==='failed'){add('ATTEMPT_FAILED',p,connection)}
      else {add('UPSTREAM_FIRST_EVENT',p,connection);add('CLIENT_FIRST_OUTPUT');if(!live.has(raw.status)){add('ATTEMPT_SUCCEEDED',p,connection);add('COMPLETE');add('CLIENT_DELIVERY_COMPLETED')}}
    });
    if(raw.status==='failed'){add('COMPLETE');add('CLIENT_DELIVERY_COMPLETED')}
    raw.connection_id=events.findLast(e=>e.type==='UPSTREAM_DISPATCHED'||e.type==='ATTEMPT_STARTED')?.connection_id;
    raw.observation.trace_revision=events.length;raw.observation.last_event_seq=events.length;
    details.set(raw.id,{client_request:raw,route_snapshot:{schema_version:1,coverage:'complete',captured_at:raw.timestamp,route_steps:steps,candidates},execution_trace_events:events,trace_revision:events.length,events_truncated:0});
  }
  for(const row of [...records].sort((a,b)=>a.timestamp.localeCompare(b.timestamp)||a.id.localeCompare(b.id))) {
    const previous=records.filter(r=>row.session_id&&r.session_id===row.session_id&&(r.timestamp<row.timestamp||(r.timestamp===row.timestamp&&r.id<row.id))).sort((a,b)=>b.timestamp.localeCompare(a.timestamp)||b.id.localeCompare(a.id))[0];
    selectPreview(row,previous);
    const item=legacy.get(row.id);
    if(item)Object.assign(item,{sessionId:row.session_id,callerIdentityId:row.caller_identity_id,clientKind:row.client_kind,contentPreview:row.content_preview,observation:row.observation,timestamp:row.timestamp,raw:{...(item.raw||{}),...row}});
  }
  const stats=(rows,provider='')=>{
    const linked=rows.filter(r=>r.session_id),groups=new Set(linked.map(r=>r.session_id)),active=rows.filter(r=>live.has(r.status));
    const known=rows.filter(r=>['known','partial'].includes(r.cost_state));
    const succeeded=rows.filter(r=>r.status==='success').length,failed=rows.filter(r=>r.status==='failed').length;
    return {sessions:groups.size,active_sessions:new Set(active.filter(r=>r.session_id).map(r=>r.session_id)).size,
      failed_sessions:new Set(linked.filter(r=>r.status==='failed').map(r=>r.session_id)).size,recovered_sessions:new Set(linked.filter(r=>r.outcome==='RECOVERED_SUCCESS').map(r=>r.session_id)).size,
      requests:rows.length,associated_requests:linked.length,independent_requests:rows.length-linked.length,active_requests:active.length,succeeded,failed,canceled:rows.filter(r=>r.status==='canceled').length,
      recovered:rows.filter(r=>r.outcome==='RECOVERED_SUCCESS').length,average_requests_per_session:groups.size?linked.length/groups.size:null,
      known_cost:known.length?sum(known,totalCost):null,cost_state:coverage(rows,'cost_state'),cost_known_requests:rows.filter(r=>r.cost_state==='known').length,cost_partial_requests:rows.filter(r=>r.cost_state==='partial').length,
      usage:{input_tokens:use(rows,'input_tokens'),output_tokens:use(rows,'output_tokens'),cache_read_tokens:use(rows,'cache_read_tokens'),cache_write_tokens:use(rows,'cache_write_tokens')},usage_state:coverage(rows,'usage_state'),
      usage_known_requests:rows.filter(r=>r.usage_state==='known').length,usage_partial_requests:rows.filter(r=>r.usage_state==='partial').length,
      participating_requests:rows.filter(r=>provider?r._participating.includes(provider):r._participating.length).length,
      pending_requests:rows.filter(r=>provider?r._pending.includes(provider):r._pending.length).length,
      association_coverage:rows.length?linked.length/rows.length:null,success_rate:succeeded+failed?succeeded/(succeeded+failed):null};
  };
  const filter=(options={})=>{
    const windowMS=({ '15m':900000,'1h':3600000,'24h':86400000,'168h':604800000,'720h':2592000000})[options.window]||86400000;
    const to=options.to?Date.parse(options.to):now+1,from=options.scope==='all'?-Infinity:options.from?Date.parse(options.from):to-windowMS;
    const exact=records.some(r=>r.id===options.q),q=String(options.q||'').toLowerCase();
    return records.filter(r=>Date.parse(r.timestamp)>=from&&Date.parse(r.timestamp)<to
      &&(!options.session_id||r.session_id===options.session_id)&&(!options.caller_id||r.caller_identity_id===options.caller_id)
      &&(!options.api_key||r.caller===options.api_key)&&(!options.logical_model||r.logical_model_id===options.logical_model)
      &&(!options.status||r.status===options.status)&&(!options.outcome||r.outcome===options.outcome)
      &&(!options.participating_provider||[...r._participating,...r._pending].includes(options.participating_provider))
      &&(!q||(exact?r.id===options.q:[r.id,r.session_id,r.caller,r.client_kind,r.content_preview.request_excerpt,r.content_preview.response_excerpt].some(s=>String(s||'').toLowerCase().includes(q)))));
  };
  const query=(options={},sessionsOnly=false)=>{
    const rows=filter(options),groups=new Map(),mode=options.mode||'session';
    for(const row of rows){if(sessionsOnly&&!row.session_id)continue;const key=mode==='session'&&row.session_id?`session:${row.session_id}`:`request:${row.id}`;if(!groups.has(key))groups.set(key,[]);groups.get(key).push(row)}
    const sources=[...groups].map(([id,items])=>{
      const ordered=items.toSorted((a,b)=>Number(b.id===options.q)-Number(a.id===options.q)||Number(live.has(b.status))-Number(live.has(a.status))||b.timestamp.localeCompare(a.timestamp)||b.id.localeCompare(a.id));
      const selected=ordered[0],first=records.filter(r=>selected.session_id&&r.session_id===selected.session_id).toSorted((a,b)=>a.timestamp.localeCompare(b.timestamp))[0]||selected;
      const recent=items.filter(r=>r.content_preview.response_excerpt).toSorted((a,b)=>b.timestamp.localeCompare(a.timestamp)||b.id.localeCompare(a.id))[0];
      const summary=id.startsWith('session:')&&recent?{...recent.content_preview,request_excerpt:first.content_preview.request_excerpt,preview_role:'response',preview_text:recent.content_preview.response_excerpt,preview_reason:'session_latest_response'}:selected.content_preview;
      const s=stats(items),participating=items.filter(r=>options.participating_provider?r._participating.includes(options.participating_provider):r._participating.length),pending=items.filter(r=>options.participating_provider?r._pending.includes(options.participating_provider):r._pending.length);
      return {source_id:id,source_kind:id.startsWith('session:')?'session':'request',id:selected.session_id||selected.id,session_id:selected.session_id,title:first.content_preview.request_excerpt||`${selected.client_kind} · ${selected.session_id||selected.id}`,
        caller_identity_id:selected.caller_identity_id,caller:selected.caller,client_kind:selected.client_kind,first_at:items.map(r=>r.timestamp).sort()[0],last_at:items.map(r=>r.timestamp).sort().at(-1),
        request_count:items.length,active_requests:s.active_requests,succeeded:s.succeeded,failed:s.failed,canceled:s.canceled,recovered:s.recovered,participating_request_count:participating.length,pending_request_count:pending.length,
        usage:s.usage,usage_state:s.usage_state,known_cost:s.known_cost,cost_state:s.cost_state,cost_scope:'participating_requests_total',logical_models:[...new Set(items.map(r=>r.logical_model_id))],provider_ids:[...new Set(items.flatMap(r=>r._participating))],
        selected_request_id:selected.id,matching_request_id:selected.id,selected_request_status:selected.status,selected_request:selected,content_preview:summary};
    }).sort((a,b)=>Number(b.active_requests>0)-Number(a.active_requests>0)||b.last_at.localeCompare(a.last_at)||a.source_id.localeCompare(b.source_id));
    const signature=JSON.stringify(Object.entries(options).filter(([key])=>!['cursor','limit'].includes(key)).sort()),limit=Number(options.limit||50);
    let offset=0;if(options.cursor){const c=JSON.parse(atob(options.cursor));if(c.signature!==signature)throw new Error('Invalid or mismatched observation cursor');offset=c.offset}
    const page=sources.slice(offset,offset+limit),next=offset+limit<sources.length?btoa(JSON.stringify({signature,offset:offset+limit})):'';
    return copy({as_of:new Date(now).toISOString(),scope:{...options,from:options.from||new Date(now-86400000).toISOString(),to:options.to||new Date(now+1).toISOString()},stats:stats(rows,options.participating_provider),coverage:{associated_requests:rows.filter(r=>r.session_id).length,independent_requests:rows.filter(r=>!r.session_id).length},total:sources.length,[sessionsOnly?'sessions':'sources']:page,next_cursor:next});
  };
  return {records,details,filter,query,legacy,
    session(id,options={}){
      const all=records.filter(r=>r.session_id===id);if(!all.length)throw new Error('Session not found');
      const page=query({...options,session_id:id,mode:'session'},true),row=page.sessions[0]||{source_id:`session:${id}`,source_kind:'session',session_id:id,title:all[0].content_preview.request_excerpt,request_count:0};
      const scoped=filter({...options,session_id:id});
      const durations=scoped.filter(r=>!live.has(r.status)&&r.duration_ms>0).map(r=>r.duration_ms).sort((a,b)=>a-b),ttft=scoped.filter(r=>r.ttft_ms>0).map(r=>r.ttft_ms).sort((a,b)=>a-b);
      const percentile=(values,p)=>values.length?values[Math.max(0,Math.ceil(values.length*p)-1)]:null;
      const attempts=scoped.flatMap(r=>details.get(r.id)?.attempts||[]);
      return {...page,session:row,metrics:{recovery_count:sum(scoped,r=>r.connection_rebind_count||r.recovery_fallback_count||Math.max(0,r.attempts-1)),recovery_added_latency_ms:sum(scoped,r=>r.recovery_added_latency_ms),provider_ids:[...new Set(attempts.map(a=>a.provider_id).filter(Boolean))],connection_ids:[...new Set(attempts.map(a=>a.connection_id).filter(Boolean))]},timing:{duration_samples:durations.length,duration_p50_ms:percentile(durations,.5),duration_p95_ms:percentile(durations,.95),ttft_samples:ttft.length,ttft_p50_ms:percentile(ttft,.5),ttft_p95_ms:percentile(ttft,.95)}};
    },
  };
}
