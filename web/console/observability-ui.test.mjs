import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {ObservationResource} from './observability-state.js';
import * as observabilityModel from './observability-model.js';

const documentEvents=new Map(),windowEvents=new Map(),intervals=[];
let now=Date.now();
class ObservationDate extends Date{static now(){return now}}
const context=vm.createContext({
  URLSearchParams,AbortController,ObservationResource,observabilityModel,queueMicrotask,
  setInterval:fn=>intervals.push(fn),setTimeout,clearTimeout,Date:ObservationDate,
  location:{hash:'#/sessions/s?from=2026-09-01T00:00:00Z&to=2026-09-02T00:00:00Z&participating_provider=p1'},
  document:{addEventListener:(name,fn)=>documentEvents.set(name,fn),activeElement:null},
  window:{addEventListener:(name,fn)=>windowEvents.set(name,fn)},
  state:{requestCache:new Map(),requests:[]},render:()=>{},consoleLocale:'en',nt:en=>en,
  esc:value=>String(value).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;'),
});
const part=await fs.readFile(new URL('./app.part15.jsfrag',import.meta.url),'utf8');
vm.runInContext(part,context);
const run=script=>vm.runInContext(script,context);
for(const content_state of ['disabled','legacy_unavailable','pending','unsupported','empty','partial']){
  context.missingPreview={content_state,preview_text:''};
  assert.equal(run('observationPreview(missingPreview)'),'',`${content_state} must not create a placeholder row`);
}
assert.equal(run('observationPreview({preview_text:"  "})'),'');
assert.match(run('observationPreview({preview_role:"response",preview_text:"Useful <answer>"})'),/<b>Response<\/b>/);
assert.match(run('observationPreview({preview_role:"response",preview_text:"Useful <answer>"})'),/Useful &lt;answer&gt;/,'real excerpts remain escaped and visible');
const first=run(`observationContext('session','s')`);
assert.equal(first.filters.participating_provider,'p1');
first.filters.q='operator draft';
assert.equal(run(`observationContext('session','s')`),first,'same entry must preserve operator filters');
first.resource.data={old:true};first.cursor='old';first.requestId='old-request';
context.location.hash='#/sessions/s?window=168h&participating_provider=p2';
assert.equal(run(`observationContext('session','s')`),first);
assert.equal(first.filters.participating_provider,'p2');assert.equal(first.filters.window,'168h');
assert.equal(first.resource.data,null,'new link scope must invalidate old cohort');assert.equal(first.cursor,'');assert.equal(first.requestId,'');

const calls=[];
context.adapter={
  getSession:async(id,filters)=>{calls.push(['session',id,filters]);return {session:{source_id:'session:s',session_id:'s',selected_request_id:'newer',request_count:2},scope:{from:'2026-09-01T00:00:00Z',to:'2026-09-02T00:00:00Z'}}},
  getSessionRequests:async(id,filters)=>{calls.push(['timeline',id,filters]);return [{id:'older'},{id:'newer'}]},
  getRequest:async(id,options)=>{calls.push(['request',id,options]);return {id,raw:{id,status:'success'}}},
};
first.requestId='older';
await run('loadObservationContext(observationContext("session","s"))');
assert.equal(first.resource.data.requestId,'older','explicit timeline request must drive full graph');
assert.equal(calls[1][2].from,'2026-09-01T00:00:00Z');assert.equal(calls[1][2].window,undefined,'timeline must share server anchored scope');
assert.equal(calls.filter(c=>c[0]==='request').length,1,'only the selected source may load full Trace');
assert.ok(calls[2][2].signal instanceof AbortSignal);

// Model routing reuses recorded request events, with the model pinned even if
// an incoming link or a retained filter names a different model.
context.location.hash='#/models/reader?logical_model=other&window=168h';
const model=run('observationContext("model","reader")');
assert.equal(model.mode,'request');
const modelCalls=[];
context.adapter={
  getProviderTraffic:async(id,filters,options)=>{modelCalls.push(['traffic',id,filters,options]);return {total:2,sources:[
    {source_id:'request:first',selected_request_id:'first',session_id:'shared-session',selected_request:{id:'first',status:'success'}},
    {source_id:'request:second',selected_request_id:'second',session_id:'shared-session',selected_request:{id:'second',status:'active'}},
  ]}},
  getRequest:async(id,options)=>{modelCalls.push(['request',id,options]);return {id,raw:{id,status:'success'}}},
  getSessionRequests:async()=>{assert.fail('model request selection must not load or substitute another Session request')},
};
await run('loadObservationContext(observationContext("model","reader"))');
assert.equal(modelCalls[0][1],'','model id must not become a Provider id');
assert.equal(modelCalls[0][2].logical_model,'reader');
assert.equal(modelCalls[0][2].mode,'request');assert.equal(modelCalls[0][2].limit,4);
assert.ok(modelCalls[0][3].signal instanceof AbortSignal);
assert.equal(model.resource.data.requests.length,2,'picker must contain the model-scoped sources');
assert.equal(model.sourceId,'request:first');
model.requestId='second';
await run('loadObservationContext(observationContext("model","reader"))');
assert.equal(model.resource.data.requestId,'second','picker must override the previous source selection');
assert.equal(model.resource.data.detail.id,'second');
assert.equal(modelCalls.filter(c=>c[0]==='request').length,2,'only the selected Request trace is fetched on each load');

// Exercise the actual selection/change/pagination handlers and polling callback.
const sourceFor=(id,modelId='reader')=>({source_id:`request:${id}`,selected_request_id:id,title:`Source ${id}`,session_id:'shared-session',selected_request:{id,logical_model_id:modelId,status:'active'}});
let pageRows=['r1','r2','r3','r4'].map(id=>sourceFor(id)),revision=0,renderedControl=null,focusCount=0;
const trafficCalls=[],detailCalls=[];
context.state.auth={authenticated:true};context.document.hidden=false;
context.adapter={
  getProviderTraffic:async(id,filters,options)=>{trafficCalls.push({id,filters,signal:options.signal});return {total:10,next_cursor:'next-page',sources:filters.logical_model==='other'?[sourceFor('other-request','other')]:pageRows}},
  getRequest:async(id,options)=>{detailCalls.push({id,signal:options.signal});return {id,traceRevision:++revision,raw:{id,logical_model_id:id==='other-request'?'other':'reader',status:'success',content_preview:{preview_text:`${id} revision ${revision}`}}}},
  getSessionRequests:async()=>assert.fail('model pinning must never expand a shared Session'),
};
function fieldControl(field,value,{select=true,open=false}={}){
  return {dataset:{observeField:field},value,open,
    matches(selector){return selector==='[data-observe-field]'||selector===`[data-observe-field="${field}"]`||(select&&selector==='select[data-observe-field]')||(selector===':open'&&this.open)},
    closest(selector){return this.matches(selector)?this:null},
    focus(){context.document.activeElement=this;focusCount++},
  };
}
const graphSlot={},graphUpdates=[];
context.app={querySelector:selector=>selector==='[data-observation-model]'?{}:selector==='[data-observation-graph]'?graphSlot:renderedControl?.matches(selector)?renderedControl:null};
context.mountRouteExplanation=(slot,detail,options)=>{
  graphUpdates.push({detail,options});return {container:slot,update:(detail,options)=>graphUpdates.push({detail,options}),relayout(){},pause(){},destroy(){}};
};
context.render=()=>{
  const current=context.document.activeElement;
  if(current?.dataset?.observeField){renderedControl=fieldControl(current.dataset.observeField,current.value);context.document.activeElement=null;}
};
run('activeObservationContext=observationContext("model","reader");resetObservationQuery(activeObservationContext)');
const poll=async()=>{intervals[0]();await model.resource.promise};
const choose=async id=>{await documentEvents.get('change')({target:fieldControl('request',id)});await model.resource.promise};
const click=action=>documentEvents.get('click')({target:{closest:selector=>selector==='[data-observe]'?{dataset:{observe:action}}:null}});
await run('loadObservationContext(activeObservationContext)');
assert.equal(model.explicitRequest,null,'automatic default selection must not be pinned');
pageRows=['r5','r6','r7','r8'].map(id=>sourceFor(id));await poll();
assert.equal(model.requestId,'r5','an automatic selection may follow the next page of fresh traffic');

pageRows=['r1','r2','r3','r4'].map(id=>sourceFor(id));run('resetObservationQuery(activeObservationContext)');await run('loadObservationContext(activeObservationContext)');
const requestControl=fieldControl('request','r2');context.document.activeElement=requestControl;
documentEvents.get('pointerdown')({target:requestControl});
const beforeOpen=trafficCalls.length;await poll();assert.equal(trafficCalls.length,beforeOpen,'native select interaction pauses polling');
await documentEvents.get('change')({target:requestControl});await model.resource.promise;
assert.equal(model.explicitRequest.id,'r2');const selectedRevision=model.resource.data.detail.traceRevision;
pageRows=['r5','r6','r7','r8'].map(id=>sourceFor(id));
await poll();await poll();await poll();
assert.equal(model.requestId,'r2','explicit Request survives eviction from the current four-row page');
assert.ok(model.resource.data.detail.traceRevision>selectedRevision,'off-page Request detail is independently refreshed');
assert.equal(model.resource.data.sources.length,4,'pinning keeps graph sources bounded');
assert.equal(model.resource.data.sources[0].source_id,'request:r2');
assert.equal(model.resource.data.sources[0].title,'Source r2','retain the model-scoped source metadata');
assert.equal(model.resource.data.sources[0].selected_request_status,'success','source state comes from refreshed detail');
assert.match(model.resource.data.sources[0].content_preview.preview_text,/revision/);
assert.equal(model.resource.data.page.sources.length,4,'pinning must not mutate the server page');
assert.match(run('observationRequestPicker(activeObservationContext)'),/value="r8"/,'all current page entries remain selectable');
assert.equal(context.document.activeElement.dataset.observeField,'request','Request selector keeps keyboard focus across polls');
assert.ok(focusCount>=3);
assert.equal(trafficCalls.length,beforeOpen+4,'change completes selection and permits every subsequent focused-select poll');
assert.ok(trafficCalls.every(call=>call.id===''&&call.filters.logical_model==='reader'&&call.filters.mode==='request'));
assert.ok(detailCalls.slice(-3).every(call=>call.id==='r2'),'polls fetch the pinned detail, not a newly arrived Request');
run('syncObservabilityView({section:"models",id:"reader"})');
assert.equal(graphUpdates.at(-1).options.sources.length,4);assert.equal(graphUpdates.at(-1).options.selectedSourceId,'request:r2');
assert.ok(graphUpdates.at(-1).options.sources.every(source=>source.selected_request.logical_model_id==='reader'),'every displayed source belongs to this logical model');
assert.equal(graphUpdates.at(-1).detail.id,'r2');
const beforeForeign=detailCalls.length;await choose('other-request');
assert.equal(detailCalls.length,beforeForeign,'a Request absent from this model cohort cannot become an explicit pin');
await choose('r8');assert.equal(model.requestId,'r8','the picker may select a page entry outside the four displayed graph sources');

const windowControl=fieldControl('window','15m');context.document.activeElement=windowControl;
const windowChange=documentEvents.get('change')({target:windowControl});
assert.equal(model.explicitRequest,null);assert.equal(model.requestId,'','changing scope clears the old explicit selection');
await windowChange;await model.resource.promise;await poll();await poll();
assert.equal(context.document.activeElement.dataset.observeField,'window','time selection also keeps focus without blocking polling');
assert.equal(trafficCalls.at(-1).filters.window,'15m');assert.equal(model.requestId,'r5');
for(const action of ['next','previous','clear']){
  await choose('r6');click(action);assert.equal(model.explicitRequest,null,`${action} clears the explicit Request`);assert.equal(model.requestId,'');
  await model.resource.promise;assert.equal(model.requestId,'r5');assert.equal(trafficCalls.at(-1).filters.logical_model,'reader');
  if(action==='next')assert.equal(trafficCalls.at(-1).filters.cursor,'next-page');
}
assert.equal(model.filters.logical_model,'reader','clear filters keeps the model scope in links and subsequent reads');

const focusedSelect=context.document.activeElement;focusedSelect.open=true;
const beforeExpanded=trafficCalls.length;await poll();assert.equal(trafficCalls.length,beforeExpanded,'an actually expanded select pauses polling');
focusedSelect.open=false;
documentEvents.get('keydown')({target:focusedSelect,key:'ArrowDown'});await poll();assert.equal(trafficCalls.length,beforeExpanded);
documentEvents.get('keydown')({target:focusedSelect,key:'Escape'});await poll();assert.equal(trafficCalls.length,beforeExpanded+1,'canceling a keyboard selection resumes polling');
documentEvents.get('pointerdown')({target:context.document.activeElement});now+=5001;await poll();
assert.equal(trafficCalls.length,beforeExpanded+2,'native-popup fallback cannot pause a focused select indefinitely');
await choose('r6');
const search=fieldControl('q','typed',{select:false});context.document.activeElement=search;
documentEvents.get('input')({target:search});const beforeTyping=trafficCalls.length;await poll();
assert.equal(model.explicitRequest,null,'editing a filter clears the explicit Request before its debounce completes');
assert.equal(trafficCalls.length,beforeTyping,'debounced input temporarily pauses polling');
run('syncObservabilityView({section:"overview"})');assert.equal(run('observationSearchTimer'),null,'navigation cancels pending input work');
context.document.activeElement=null;

// Ignore adapters that resolve an old, aborted off-page detail after a filter
// replacement, a new entry scope, or navigation to another model.
const getDetail=context.adapter.getRequest;
for(const cancellation of ['filter','entry','navigation']){
  context.location.hash='#/models/reader?window=24h';
  run('syncObservabilityView({section:"models",id:"reader"});resetObservationQuery(activeObservationContext)');
  pageRows=['r1','r2','r3','r4'].map(id=>sourceFor(id));await run('loadObservationContext(activeObservationContext)');await choose('r2');
  const priorDetail=model.resource.data.detail,priorCache=context.state.requestCache.get('r2');
  let delayed;context.adapter.getRequest=(id,options)=>id==='r2'?new Promise(resolve=>{delayed={resolve,signal:options.signal}}):getDetail(id,options);
  pageRows=['r5','r6','r7','r8'].map(id=>sourceFor(id));const pendingPin=run('loadObservationContext(activeObservationContext)');
  await new Promise(resolve=>setImmediate(resolve));assert.ok(delayed,'poll must independently read the off-page pin');
  if(cancellation==='filter'){
    await documentEvents.get('change')({target:fieldControl('window','1h')});await model.resource.promise;
  }else if(cancellation==='entry'){
    context.location.hash='#/models/reader?window=1h';run('observationContext("model","reader")');await run('loadObservationContext(activeObservationContext)');
  }else{
    context.location.hash='#/models/other';run('syncObservabilityView({section:"models",id:"other"})');await run('loadObservationContext(activeObservationContext)');
  }
  assert.equal(delayed.signal.aborted,true);
  delayed.resolve({id:'r2',raw:{id:'r2',status:'active'},traceRevision:9999});await pendingPin;
  assert.equal(context.state.requestCache.get('r2'),priorCache,'aborted pin cannot revive stale cached detail');
  if(cancellation==='navigation'){
    assert.equal(model.resource.data.detail,priorDetail,'leaving the model retains only the last committed result');
    assert.equal(run('activeObservationContext.resource.data.requestId'),'other-request','late old-model detail cannot replace the new model');
  }else{assert.equal(model.requestId,'r5');assert.equal(model.explicitRequest,null);}
  context.adapter.getRequest=getDetail;
}
run('syncObservabilityView({section:"overview"})');context.document.activeElement=null;context.render=()=>{};

const modelPage=await fs.readFile(new URL('./app.part3.jsfrag',import.meta.url),'utf8');
vm.runInContext(modelPage.slice(modelPage.indexOf('function modelRoutingChain('),modelPage.indexOf('function pageModel(')),context);
context.state.providers=[{id:'p1',name:'Provider <one>'}];context.icon=()=>'';
context.planModel={id:'reader',name:'Reader <test>'};
context.planRoutes=[{id:'p1',model:'glm-5.3',recoverOn:[]}];
context.planOverrides=[{id:'rule"one',name:'Weekday <rule>',enabled:false,mode:'custom_route',route:{steps:[
  {provider_id:'p1',upstream_model:'gpt-6.1-sol',recover_on:['rate_limit']},
  {provider_id:'p2',upstream_model:'gpt-6.1-sol'},
]}}];
context.overrideMatchSummary=()=>'<condition>';context.overrideActionSummary=()=>'<adjustment>';
const plan=run('modelRoutingPlan(planModel,planRoutes,planOverrides)');
assert.match(plan,/is-disabled/);assert.match(plan,/Disabled/);assert.doesNotMatch(plan,/>Active</,'enabled configuration must not imply live execution');
assert.match(plan,/Provider &lt;one&gt;/);assert.match(plan,/Reader &lt;test&gt;/);assert.match(plan,/rule&quot;one/);
assert.match(plan,/rate_limit/);assert.match(plan,/glm-5.3/);assert.match(plan,/gpt-6.1-sol/);
assert.match(plan,/data-action="edit-routing"/);assert.match(plan,/data-action="edit-override"/);
assert.match(run('modelRoutingChain([])'),/No route configured/);
assert.match(run('modelRoutingChain([{id:"p1",model:"one"},{id:"p2",model:"two"}])'),/No eligible connection \/ incompatible/,'empty recover_on must disclose automatic eligibility and compatibility fallback without promising other failure progression');
run('activeObservationContext=observationContext("session","s")');
context.render=()=>{};
const focused={dataset:{observeField:'q'},selectionStart:3,selectionEnd:8};context.document.activeElement=focused;
let restored=[];context.app={querySelector:()=>({focus:()=>restored.push('focus'),setSelectionRange:(...range)=>restored.push(range)})};
run('renderObservationPreservingFocus()');assert.deepEqual(restored,['focus',[3,8]],'async rendering must preserve search keyboard focus/caret');

const detailSource=await fs.readFile(new URL('./app.part5.jsfrag',import.meta.url),'utf8');
const start=detailSource.indexOf('const requestDetailLoads=new Map()'),end=detailSource.indexOf('\nfunction conversationRequestsSort',start);
assert.ok(start>=0&&end>start);
const pending=[];let route={section:'requests',id:'one'},renders=0;
context.currentRoute=()=>route;context.render=()=>renders++;context.showToast=()=>{};
context.adapter={getRequest:(id,{signal})=>new Promise(resolve=>pending.push({id,signal,resolve}))};
vm.runInContext(detailSource.slice(start,end),context);
const one=run('loadAndRenderRequest("one")'),duplicate=run('loadAndRenderRequest("one")');
await Promise.resolve();assert.equal(pending.length,1,'slow polling must stay single flight');
route={section:'requests',id:'two'};const two=run('loadAndRenderRequest("two")');await Promise.resolve();
assert.equal(pending[0].signal.aborted,true);
pending[1].resolve({id:'two'});await two;
pending[0].resolve({id:'one',stale:true});await Promise.all([one,duplicate]);
assert.equal(context.state.requestCache.get('one'),undefined,'late request must not restore old selection');assert.equal(context.state.requestCache.get('two').id,'two');assert.equal(renders,1);
console.log('Observation navigation, scoped selection, focus and Request polling: PASS');
