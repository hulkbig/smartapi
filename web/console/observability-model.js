// Pure projections of persisted observations. No current Provider configuration
// or client-side candidate inference is used to reconstruct historical routes.
export const liveRequestStatuses = new Set(['received','waiting','active','recovering','streaming','delivering']);

export function previewOf(request = {}) {
  const raw=request.raw || request;
  const preview=request.contentPreview || raw.content_preview || raw.observation?.content_preview || {};
  return {request_excerpt:'',response_excerpt:'',preview_role:'',preview_text:'',content_state:'legacy_unavailable',...preview};
}

export function previewLabel(preview, locale='en') {
  const role=preview?.preview_role;
  if(role==='request')return 'Request';
  if(role==='response')return 'Response';
  if(role==='tool')return 'Tool';
  return locale.startsWith('zh')?'内容':'Content';
}

export function observationQuery(filters={}) {
  const query=new URLSearchParams();
  for(const [key,value] of Object.entries(filters)) {
    if(value==null || value==='' || key==='signal')continue;
    if(key==='window' && (filters.scope==='all' || (filters.from && filters.to)))continue;
    if(filters.scope==='all' && ['from','to'].includes(key))continue;
    query.set(key,String(value));
  }
  if(!query.has('window')&&!query.has('from')&&!query.has('to')&&!query.has('scope'))query.set('window','24h');
  return query.toString();
}

export function eventOrder(events=[]) {
  const seen=new Set();
  return events.map((event,index)=>({...event,_index:index})).filter(event=>{
    if(!Number.isSafeInteger(event.seq)||event.seq<=0)return true;
    if(seen.has(event.seq))return false;seen.add(event.seq);return true;
  }).sort((a,b)=>(a.seq>0 && b.seq>0 ? a.seq-b.seq : a._index-b._index));
}

const activeKey = (provider,connection,step) => `${step||0}\0${provider||''}\0${connection||''}`;

export function buildRouteViewModel(detail={},selectedSeq=Infinity) {
  const request=detail.client_request || detail.raw || detail;
  const snapshot=detail.routeSnapshot || detail.route_snapshot || request.route_snapshot || {};
  const candidates=(snapshot.candidates || []).map(candidate=>({...candidate,conditions:(candidate.conditions||[]).map(condition=>({...condition}))}));
  const events=eventOrder(detail.executionEvents || detail.execution_trace_events || []);
  const visible=events.filter(event=>!Number.isFinite(selectedSeq) || (event.seq>0?event.seq:event._index+1)<=selectedSeq);
  const nodes=new Map();
  for(const step of snapshot.route_steps || []) {
    const key=`${step.route_step}:${step.provider_id}`;
    nodes.set(key,{...step,key,provider_name:step.provider_name||step.provider_id||'Unknown Provider',candidates:[],active:false,selected:false,attempted:false,failed:false});
  }
  for(const candidate of candidates) {
    const key=`${candidate.route_step||0}:${candidate.provider_id||'unknown'}`;
    if(!nodes.has(key))nodes.set(key,{key,route_step:candidate.route_step||0,provider_id:candidate.provider_id||'',provider_name:candidate.provider_name||candidate.provider_id||'Historical candidate',candidates:[],active:false,selected:false,attempted:false,failed:false});
    nodes.get(key).candidates.push(candidate);
  }
  const latest=!Number.isFinite(selectedSeq);
  const status=request.status || String(detail.result||'').toLowerCase();
  let phase='unknown',selected=null,dispatched=false,delivering=false,gatewayWork=null,packet=null,lastEvent=null;
  const attempted=new Set(),failures=new Set();
  for(const event of visible) {
    lastEvent=event;packet=null;
    if(event.provider_id && event.connection_id && ['CONNECTION_SELECTED','CONNECTION_REBOUND','ROUTE_FALLBACK','ATTEMPT_STARTED','ADMISSION_COMPLETED','UPSTREAM_DISPATCHED','UPSTREAM_FIRST_EVENT'].includes(event.type)) {
      selected={provider:event.provider_id,connection:event.connection_id,step:event.route_step||selected?.step||0};
    }
    switch(event.type) {
      case 'REQUEST_RECEIVED':phase='received';packet='client-request';gatewayWork='receiving';break;
      case 'BODY_RECEIVE_STARTED':phase='receiving';gatewayWork='receiving';break;
      case 'BODY_RECEIVE_COMPLETED':phase='received';gatewayWork=null;break;
      case 'BODY_PARSE_STARTED':phase='parsing';gatewayWork='parsing';break;
      case 'BODY_PARSE_COMPLETED':phase='parsed';gatewayWork=null;break;
      case 'PLANNING_STARTED':case 'MODEL_RESOLVED':case 'ROUTE_RESOLVED':case 'COMPATIBILITY_STARTED':phase='planning';gatewayWork='planning';break;
      case 'PLANNING_COMPLETED':case 'COMPATIBILITY_COMPLETED':phase='planned';gatewayWork=null;break;
      case 'CONNECTION_SELECTED':case 'ATTEMPT_STARTED':case 'CONVERSATION_NATIVE':case 'CONVERSATION_REHYDRATING':
        phase='preparing';dispatched=false;gatewayWork='preparing';break;
      case 'ADMISSION_COMPLETED':phase='admitted';gatewayWork='preparing';break;
      case 'UPSTREAM_DISPATCHED':
        phase='calling';dispatched=true;gatewayWork=null;packet='upstream-request';
        attempted.add(activeKey(event.provider_id,event.connection_id,event.route_step));break;
      case 'UPSTREAM_FIRST_EVENT':phase='upstream-response';packet='upstream-response';break;
      case 'ADMISSION_FAILED':phase='admission-failed';dispatched=false;gatewayWork=null;break;
      case 'ATTEMPT_FAILED':
        phase=dispatched?'upstream-failed':'admission-failed';packet=dispatched?'upstream-error':null;dispatched=false;gatewayWork=null;
        failures.add(activeKey(event.provider_id,event.connection_id,event.route_step||selected?.step));break;
      case 'CONNECTION_REBOUND':case 'ROUTE_FALLBACK':case 'PROVIDER_EXHAUSTED':phase='recovering';dispatched=false;gatewayWork='recovering';break;
      case 'RECOVERY_STOPPED':phase='recovery-stopped';dispatched=false;gatewayWork=null;break;
      case 'ATTEMPT_SUCCEEDED':
        phase='upstream-complete';dispatched=false;gatewayWork=null;packet='upstream-response';
        failures.delete(activeKey(event.provider_id,event.connection_id,event.route_step||selected?.step));break;
      case 'CLIENT_FIRST_OUTPUT':phase='delivering';delivering=true;packet='client-response';break;
      case 'CLIENT_DELIVERY_COMPLETED':
        phase=status==='canceled'?'canceled':status==='failed'?'failed':'complete';dispatched=false;delivering=false;gatewayWork=null;packet=phase==='canceled'?null:phase==='failed'?'client-error':'client-response';break;
      case 'CLIENT_DELIVERY_INTERRUPTED':case 'INGRESS_CANCELED':case 'PLANNING_CANCELED':case 'RUNTIME_RESTARTED':phase='canceled';dispatched=false;packet=null;break;
      // Scheduler completion can precede the last downstream write.
      case 'COMPLETE':if(!['failed','canceled','complete'].includes(phase))phase='processed';dispatched=false;gatewayWork=null;break;
      case 'BODY_RECEIVE_FAILED':case 'BODY_PARSE_FAILED':case 'INGRESS_FAILED':case 'PLANNING_FAILED':phase='failed';dispatched=false;break;
    }
    if(['failed','canceled','complete'].includes(phase)){gatewayWork=null;delivering=false;}
  }
  // A waiting row may precede its matching trace publication, but is never
  // evidence that an upstream call is active. Current row state must not leak
  // backwards into an explicit historical replay.
  if(latest && status==='waiting') {phase='waiting';packet=null;dispatched=false;delivering=false;gatewayWork='waiting';}
  if(latest && ['success','failed','canceled'].includes(status)) {
    phase=status==='success'?(visible.some(event=>event.type==='CLIENT_DELIVERY_COMPLETED')?'complete':'succeeded'):status;dispatched=false;delivering=false;gatewayWork=null;
    if(lastEvent?.type!=='CLIENT_DELIVERY_COMPLETED')packet=null;
  }
  if(delivering)phase='delivering';
  const matches=(candidate,keySet)=>keySet.has(activeKey(candidate.provider_id,candidate.connection_id,candidate.route_step)) || keySet.has(activeKey(candidate.provider_id,candidate.connection_id,0));
  for(const node of nodes.values()) {
    for(const candidate of node.candidates) {
      candidate.selected=Boolean(selected && candidate.provider_id===selected.provider && candidate.connection_id===selected.connection && (!selected.step || candidate.route_step===selected.step));
      candidate.active=candidate.selected && dispatched;
      candidate.attempted=matches(candidate,attempted);candidate.failed=matches(candidate,failures);
      node.selected ||= candidate.selected;node.active ||= candidate.active;node.attempted ||= candidate.attempted;node.failed ||= candidate.failed;
    }
  }
  return {requestId:request.id || detail.id || '',sessionId:request.session_id || detail.sessionId || null,
    revision:detail.traceRevision ?? detail.trace_revision ?? request.observation?.trace_revision ?? 0,
    coverage:snapshot.coverage || 'legacy',providers:[...nodes.values()],candidateCount:candidates.length,
    events,visibleEvents:visible,lastEvent,phase,packet,selected,dispatched,
    work:{gateway:delivering?'delivering':gatewayWork,source:delivering?'delivering':null,upstream:dispatched},
    eventsTruncated:Number(detail.eventsTruncated ?? detail.events_truncated ?? 0),
    live:liveRequestStatuses.has(status),
    preview:previewOf(request)};
}

export function nullableNumber(value) {
  return value===null || value===undefined || value==='' || !Number.isFinite(Number(value)) ? null : Number(value);
}

export function sourceRequestId(source) {
  return source?.selected_request_id || source?.matching_request_id || source?.selected_request?.id || '';
}

// Session totals and titles remain Session-scoped. The highlighted card's
// request status and excerpt always describe the explicitly selected request.
export function focusedSource(source, detail) {
  const request=detail?.client_request || detail?.raw || detail;
  if(!source || !request?.id)return source;
  return {...source,selected_request_id:request.id,selected_request:request,
    selected_request_status:request.status || String(detail.result||'').toLowerCase(),
    content_preview:previewOf(request)};
}
