import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {ObservationResource} from './observability-state.js';
import * as observabilityModel from './observability-model.js';

const documentEvents=new Map(),windowEvents=new Map();
const context=vm.createContext({
  URLSearchParams,AbortController,ObservationResource,observabilityModel,queueMicrotask,
  setInterval:()=>0,setTimeout,clearTimeout,
  location:{hash:'#/sessions/s?from=2026-09-01T00:00:00Z&to=2026-09-02T00:00:00Z&participating_provider=p1'},
  document:{addEventListener:(name,fn)=>documentEvents.set(name,fn),activeElement:null},
  window:{addEventListener:(name,fn)=>windowEvents.set(name,fn)},
  state:{requestCache:new Map(),requests:[]},render:()=>{},consoleLocale:'en',
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
