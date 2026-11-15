import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {HttpSmartAPIAdapter,MockSmartAPIAdapter} from './api.js';
const parts=await Promise.all(Array.from({length:15},(_,i)=>fs.readFile(new URL(`./app.part${i+1}.jsfrag`,import.meta.url),'utf8')));
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const state={wizard:{},apiKeys:[],providers:[],managementTokens:[{id:'token',name:'Automation'}],modelCache:new Map(),providerCache:new Map(),requestCache:new Map(),providerCodexUsage:new Map(),overviewWindow:'15m'};
const clicks={},inputs={},toasts=[];
const adapter=new MockSmartAPIAdapter();
const context=vm.createContext({state,adapter,Date,Intl,URL,URLSearchParams,AbortController,queueMicrotask,setTimeout,clearTimeout,
 app:{addEventListener:(event,handler)=>clicks[event]=handler,querySelectorAll:()=>[]},portal:{addEventListener:(event,handler)=>inputs[event]=handler},
 document:{addEventListener(){}},window:{addEventListener(){}},location:{hash:''},
 ensureModel:id=>adapter.getModel(id),render(){},renderPortal(){},closeOverlay(){},confirmConsole:()=>true,
 showToast:(message,tone)=>toasts.push({message,tone}),resourceHash:(section,id)=>`#/${section}/${encodeURIComponent(id)}`,
 nt:en=>en,esc:value=>String(value??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;'),
 refreshApiKeysInBackground:async()=>{},portalViewSnapshot:()=>null,restorePortalView(){},currentRoute:()=>({section:'models',id:'exact'}),
 FormData:class{constructor(form){this.form=form}get(name){return this.form.values[name]||''}},
});
const run=code=>vm.runInContext(code,context);
function loadListener(source,marker){const start=source.indexOf(marker),end=source.indexOf('\n});',start);assert.ok(start>=0&&end>start);run(source.slice(start,end+4))}
loadListener(parts[9],"app.addEventListener('click'");loadListener(parts[10],"portal.addEventListener('change'");
const click=dataset=>clicks.click({target:{closest:()=>({dataset})}});
const change=(wizard,checked)=>inputs.change({target:{dataset:{wizard},checked}});
run(parts[6].slice(parts[6].indexOf('function managementTokenLocalExpiry('),parts[6].indexOf('function ferrySkillInstallPrompt(')));
run(parts[10].slice(parts[10].indexOf('async function submitAuthForm('),parts[10].indexOf('\napp.addEventListener',parts[10].indexOf('async function submitAuthForm('))));

await adapter.createModel({id:'exact',display_name:'Exact',enabled:false,exact_only:true,default_route:{steps:[]}});
await click({action:'edit-model',id:'exact'});assert.equal(state.wizard.model.exactOnly,true);
await change('model-exact-only',false);await click({action:'model-save',mode:'edit'});
assert.equal((await adapter.getModel('exact')).model.raw.exact_only,false);
await click({action:'edit-model',id:'exact'});await change('model-exact-only',true);await click({action:'model-save',mode:'edit'});
assert.equal((await adapter.getModel('exact')).model.raw.exact_only,true);
let policy;
adapter.getApiKey=async()=>({id:'k',name:'Key',identity:{exact_only:true,scope_models:[],scope_protocols:[],expires_at:'2030-11-13T10:00:47.000Z'}});
adapter.updateApiKey=async(_id,value)=>{policy=value};adapter.getApiKeys=async()=>[];
for(const timezone of ['Asia/Shanghai','America/New_York','UTC']){
 process.env.TZ=timezone;
 await click({action:'edit-key-policy',id:'k'});assert.equal(state.wizard.keyPolicy.exactOnly,true);
 await change('policy-exact-only',false);await click({action:'key-policy-save',id:'k'});
 assert.equal(policy.exact_only,false);assert.equal(policy.expires_at,'2030-11-13T10:00:47.000Z',timezone+' must preserve the exact instant, including seconds');
}
await click({action:'create-key'});state.wizard.key.name='New';await change('key-exact-only',true);
let created;
adapter.createApiKey=async value=>{created=value;return{key:'synthetic-key'}};
await click({action:'key-create-submit'});assert.equal(created.exact_only,true);assert.equal(state.modal.key,'synthetic-key');
const http=new HttpSmartAPIAdapter();
assert.equal(http.mapApiKey({identity:{enabled:true,expires_at:'2000-01-01T00:00:00Z'}}).status,'Expired');
assert.equal(http.mapApiKey({identity:{enabled:true,revoked_at:'2000-01-01T00:00:00Z',expires_at:'2000-01-01T00:00:00Z'}}).status,'Revoked');
console.log('PASS model/key exact compatibility, timezone-safe expiry and expired state');

// One-time secrets are revealed before the independent list refresh.
context.refreshManagementSecurity=async()=>{throw new Error('list unavailable')};
let createCalls=0,finish;
adapter.createManagementToken=()=>{createCalls++;return new Promise(resolve=>finish=resolve)};
const button={disabled:false},form={dataset:{form:'management-token-create'},values:{name:'Automation'},querySelector:()=>button};
const first=context.submitAuthForm(form),duplicate=context.submitAuthForm(form);
assert.equal(createCalls,1);assert.equal(button.disabled,true);
finish({secret:'synthetic-one-time-secret',token:{name:'Automation'}});await Promise.all([first,duplicate]);await tick();
assert.equal(state.modal.secret,'synthetic-one-time-secret');assert.equal(state.modal.type,'management-token-reveal');assert.equal(button.disabled,false);
assert.match(toasts.at(-1).message,/list refresh failed/);
adapter.rotateManagementToken=async()=>({secret:'synthetic-rotated-secret'});
await click({action:'management-token-rotate',id:'token'});await tick();assert.equal(state.modal.secret,'synthetic-rotated-secret');
console.log('PASS duplicate submission guard and one-time token result survival');

// Shared membership, not equal numbers or names, controls frontend cache reuse.
run(parts[3].slice(parts[3].indexOf('function providerIsCodex('),parts[3].indexOf('function codexUsagePercent(')));
const connection=(id,group)=>({id,quotaGroupId:group,models:[{protocols:[id]}]});
state.providers=[{id:'p',raw:{type:'custom',base_url:'https://open.bigmodel.cn/api/coding/paas/v4'},connectionItems:[connection('chat','shared'),connection('responses','shared'),connection('anthropic','shared'),connection('independent','other')]},{id:'p2',connectionItems:[connection('other-provider','shared')]}];
assert.equal(context.providerUsageGroups(state.providers[0]).length,2);
const a=context.providerCodexUsageEntry('p','chat');assert.equal(a,context.providerCodexUsageEntry('p','responses'));assert.equal(a,context.providerCodexUsageEntry('p2','other-provider'));
assert.notEqual(a,context.providerCodexUsageEntry('p','independent'));
let calls=0,resolveQuota;
adapter.getConnectionUsage=(_provider,_connection,options)=>{calls++;assert.equal(options.quotaGroupId,'shared');return new Promise(resolve=>resolveQuota=resolve)};
const reads=['chat','responses','anthropic'].map(id=>context.loadSharedCodexUsage('p',id,{refresh:true}));await tick();assert.equal(calls,1);
resolveQuota({windows:[{used_percent:20}]});await Promise.all(reads);context.invalidateProviderCodexUsage('p','chat');
assert.notEqual(a,context.providerCodexUsageEntry('p2','other-provider'));
console.log('PASS protocol and cross-provider quota cache sharing and invalidation');

// Old query rows must never acquire a new query label after a failed load.
run(parts[3].slice(parts[3].indexOf('function detailLoadState('),parts[3].indexOf('function providerIsEnabled(')));
run(parts[4].slice(parts[4].indexOf('let requestLoadGeneration='),parts[4].indexOf('function requestDataSync')));
let query={q:'old',limit:25,offset:0};context.requestFilters=()=>query;
state.requests=[{id:'old'}];state.requestLastUpdated=new Date();state.requestQueryKey=JSON.stringify(query);
adapter.getRequests=async()=>{throw new Error('unavailable')};await context.reloadRequests();assert.equal(state.requests[0].id,'old');assert.equal(state.requestError.stale,true);
query={...query,q:'new'};await context.reloadRequests();assert.equal(state.requests.length,0);assert.equal(state.requestError.stale,false);
let detailCalls=0;adapter.getModel=async()=>{detailCalls++;throw Object.assign(new Error('deleted'),{status:404})};
await context.loadAndRenderModel('missing');await context.loadAndRenderModel('missing');assert.equal(detailCalls,1);assert.equal(context.detailLoadState('models','missing').error.status,404);
console.log('PASS scoped failures and persistent detail failure without reload loops');

// Provider HTTP cache only coalesces in-flight reads; later refreshes fetch again.
let rawCalls=0,resolveRaw;http.request=()=>{rawCalls++;return new Promise(resolve=>resolveRaw=resolve)};
const raw1=http.loadProvidersRaw(),raw2=http.loadProvidersRaw();assert.equal(rawCalls,1);resolveRaw({providers:[]});await Promise.all([raw1,raw2]);
const raw3=http.loadProvidersRaw();assert.equal(rawCalls,2);resolveRaw({providers:[]});await raw3;
let expired=0;const unauthorized=new HttpSmartAPIAdapter('',async()=>({ok:false,status:401,text:async()=>'{"error":"unauthorized"}'}));
unauthorized.onUnauthorized=()=>expired++;
await assert.rejects(unauthorized.request('/api/providers'));assert.equal(expired,1);
await assert.rejects(unauthorized.request('/api/auth/login'));assert.equal(expired,1);
console.log('PASS provider refresh freshness and centralized session expiry callback');

// Restore is a reviewable draft; only the explicit apply uses the reviewed CAS.
const restore=new HttpSmartAPIAdapter(),restoreCalls=[];
restore.request=async(path,options={})=>{restoreCalls.push({path,options});if(path.endsWith('/restore-draft'))return{id:'draft-a',base_revision:17,intent:{providers:[{id:'p',enabled:false}]}};if(path.endsWith('/validate'))return{valid:true,warnings:[]};if(path==='/api/revisions/17')return{intent:{providers:[{id:'p',enabled:true}]}};if(path.endsWith('/apply'))return{revision:18};throw new Error(path)};
const review=await restore.prepareRevisionRestore(3);assert.equal(restoreCalls.length,3);assert.ok(!restoreCalls.some(x=>x.path.endsWith('/apply')));assert.equal(review.before.providers[0].enabled,true);assert.equal(review.after.providers[0].enabled,false);
await restore.applyRevisionRestore(review);assert.equal(restoreCalls.at(-1).path,'/api/drafts/draft-a/apply');assert.deepEqual(JSON.parse(restoreCalls.at(-1).options.body),{expected_revision:17});
const invalidRestore=new HttpSmartAPIAdapter(),invalidCalls=[];
invalidRestore.request=async path=>{invalidCalls.push(path);return path.endsWith('/restore-draft')?{id:'invalid-draft',base_revision:17}: {valid:false,errors:['missing provider']}};
await assert.rejects(invalidRestore.prepareRevisionRestore(3),error=>error.name==='SmartAPIError'&&error.status===422&&error.details[0]==='missing provider');
assert.equal(invalidCalls.length,2);assert.ok(invalidCalls.every(path=>!path.endsWith('/apply')));
run(parts[8].slice(parts[8].indexOf('function configDiffRows('),parts[8].indexOf('function revisionRestoreModal(')));
const diffs=context.configDiffRows([{id:'b'},{id:'a'}],[{id:'a'},{id:'b'}],'steps');assert.ok(diffs.some(x=>x.path==='steps.$order'),'reordering cannot disappear in a keyed object diff');
assert.doesNotMatch(context.configDiffTable({name:'<script>bad</script>'},{name:'Good'}),/<script>/);
console.log('PASS restore review/current-v-target diff, order changes and exact CAS apply');

run(parts[10].slice(parts[10].indexOf('function requestTimingSegments('),parts[10].indexOf('function pageRequestDetail(')));
const at=ms=>new Date(Date.UTC(2026,10,12)+ms).toISOString();
const events=[{type:'ATTEMPT_STARTED',attempt:1,at:at(0)},{type:'ADMISSION_COMPLETED',attempt:1,at:at(100)},{type:'UPSTREAM_DISPATCHED',attempt:1,at:at(120)},{type:'UPSTREAM_FIRST_EVENT',attempt:2,at:at(200)},{type:'UPSTREAM_FIRST_EVENT',attempt:1,at:at(300)},{type:'BODY_PARSE_COMPLETED',at:at(400)},{type:'PLANNING_STARTED',at:at(500)},{type:'PLANNING_COMPLETED',at:at(450)}];
const timing=context.requestTimingSegments(events);assert.equal(timing.length,2);assert.equal(timing[0].duration,100);assert.equal(timing[1].duration,180);assert.equal(timing[1].label,'First response');
assert.equal(context.requestTimingSegments([{type:'UPSTREAM_FIRST_EVENT',at:at(2)}]).length,0);
console.log('PASS timing uses real matching-attempt boundaries and omits missing/reversed evidence');

// Multi-scope picker preserves unknown saved model IDs and updates the real draft.
const checks=[{value:'a',checked:false},{value:'b',checked:false}],all={checked:true},picker={querySelector:()=>all,querySelectorAll:()=>checks};
state.wizard.keyPolicy={models:'',protocols:''};
const scopeChange=async(value,checked,isAll=false)=>inputs.change({target:{dataset:{scopeOwner:'keyPolicy',scopeField:'models'},value,checked,hasAttribute:()=>isAll,closest:()=>picker}});
await scopeChange('a',true);assert.equal(state.wizard.keyPolicy.models,'a');assert.equal(all.checked,false);
await scopeChange('b',true);assert.equal(state.wizard.keyPolicy.models,'a, b');
await scopeChange('a',false);assert.equal(state.wizard.keyPolicy.models,'b');
await scopeChange('',true,true);assert.equal(state.wizard.keyPolicy.models,'');assert.equal(all.checked,true);
console.log('PASS multi-select scopes, deselection and explicit all-model access');
