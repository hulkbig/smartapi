import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import { HttpSmartAPIAdapter, MockSmartAPIAdapter, SmartAPIAdapter } from './api.js';

const [part4,part5,part12,part14]=await Promise.all([4,5,12,14].map(part=>fs.readFile(new URL(`./app.part${part}.jsfrag`,import.meta.url),'utf8')));
const esc=value=>String(value??'').replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const provider=(id,url,type='custom',connection={})=>({
  id,name:id,type,status:'Healthy',raw:{type,base_url:url},
  connectionItems:[{id:id+'-key',name:'API key',enabled:true,status:'Serving',...connection}],
});
const kimi=provider('kimi','https://api.kimi.com/coding/v1');
const glm=provider('glm','https://open.bigmodel.cn/api/anthropic/v1');
const zai=provider('zai','https://api.z.ai/api/v1/responses');
const ordinary=provider('GLM Kimi Coding','https://open.bigmodel.cn/api/paas/v4');
const codex=provider('codex','https://chatgpt.com/backend-api/codex','cliproxy-codex');
const kimiUsage={provider:'kimi-coding',observed_at:'2026-11-09T01:00:00Z',windows:[
  {id:'limit_5h',label:'5-hour limit',used_percent:25,unit:'percent',reset_at:1794193200},
  {id:'limit_7d',label:'Weekly limit',used_percent:0,unit:'percent'},
  {id:'limit_month_total',label:'Monthly limit',used_percent:70,unit:'percent',breakdown:[{id:'coding',label:'Monthly Coding usage',used_percent:5}]},
]};
const glmUsage={provider:'glm-coding',plan_name:'GLM Max',observed_at:kimiUsage.observed_at,windows:[
  {label:'Coding limit',used_percent:72,unit:'percent'},
  {label:'Coding limit',used:0,remaining:100,limit:100,unit:'credits'},
  {label:'MCP calls limit',used:5,remaining:95,limit:100,unit:'requests'},
]};
const zaiUsage={...glmUsage,provider:'zai-coding',plan_name:''};
const codexUsage={plan_type:'pro',rate_limit:{primary_window:{used_percent:25}},rate_limit_reset_credits:{available_count:0}};
const adapter=new SmartAPIAdapter();
for(const [endpoint,kind] of [
  ['https://api.kimi.com/coding','kimi-coding'],['https://API.KIMI.COM:443/coding/v1','kimi-coding'],
  ['https://api.kimi.ai/coding/v1/messages','kimi-coding'],
  ...['open.bigmodel.cn','api.z.ai'].flatMap(host=>['coding/paas/v4','anthropic/v1','v1','v1/responses'].map(path=>[
    `https://${host}/api/${path}`,host==='api.z.ai'?'zai-coding':'glm-coding',
  ])),
]){
  assert.equal(adapter.codingUsageKind(provider('test',endpoint)),kind,endpoint);
}
const userinfoEndpoint = new URL('https://api.kimi.com/coding/v1');
userinfoEndpoint.username = 'u';
userinfoEndpoint.password = 'p';
for(const endpoint of [
  'https://api.kimi.com/v1','https://open.bigmodel.cn/api/paas/v4','https://api.z.ai/api/paas/v4',
  'https://ark.cn-beijing.volces.com/api/coding/v3','http://api.kimi.com/coding/v1',
  'https://api.kimi.com.evil.test/coding/v1','https://api.kimi.com/coding-other','https://api.z.ai/api/v10',
  'https://api.kimi.com:8443/coding/v1',userinfoEndpoint.href,'https://@api.kimi.com/coding/v1',
  'https://api.kimi.com/coding/v1?','https://api.kimi.com/coding/v1#','https://api.kimi.com/cod%69ng/v1',
  'https://api.kimi.com/v1/../coding/v1','not a URL',
]){
  assert.equal(adapter.codingUsageKind(provider('test',endpoint)),'',endpoint);
}
assert.equal(adapter.codingUsageKind(provider('test',kimi.raw.base_url,'')),'kimi-coding');
assert.equal(adapter.codingUsageKind({...kimi,raw:{...kimi.raw,type:'cliproxy-codex'}}),'','raw type takes precedence over display type');
assert.equal(adapter.codingUsageKind(kimi,{baseUrl:'https://api.kimi.com/v1'}),'','connection overrides the provider base URL');
assert.equal(adapter.codingUsageKind(kimi,{baseUrl:kimi.raw.base_url,fullUrl:'https://api.kimi.com/v1/messages'}),'','full URL overrides the connection base URL');
assert.equal(adapter.codingUsageKind(ordinary,{raw:{base_url:'https://example.test',full_url:zai.raw.base_url}}),'zai-coding');
console.log('PASS official Coding Plan endpoint eligibility and connection URL precedence');

const calls=[];
const snapshots=new Map([[kimi.id,kimiUsage],[glm.id,glmUsage],[zai.id,zaiUsage],[codex.id,codexUsage]]);
const getUsage=async(provider,connection,{refresh})=>{
  calls.push([provider,connection,refresh]);
  return refresh?snapshots.get(provider):null;
};
adapter.getConnectionUsage=getUsage;
adapter.getConnectionOAuthUsage=()=>assert.fail('Console usage must use the shared adapter');
const state={providerCodexUsage:new Map(),providerCache:new Map(),providers:[kimi,glm,zai,ordinary],models:[],providerSearch:'',usageResetRequests:[]};
const context=vm.createContext({
  state,adapter,URL,Date,Intl,queueMicrotask,esc,nt:en=>en,resourceHash:(section,id)=>`#/${section}/${id}`,fmtPct:v=>v+"%",detailFailure:()=>"",consoleLocale:'en-US',demoMode:false,
  icon:()=>'<svg/>',badge:value=>`<span>${esc(value)}</span>`,scenarioView:()=>({}),scenarioBanner:()=>'',
  portalViewSnapshot:()=>null,app:{querySelectorAll:()=>[]},render(){},restorePortalView(){},
  nextDetailTabs:()=>'',nextDetailSelected:(_section,_id,tab)=>tab==='accounts',nextAnalytics:()=>'<analytics/>',
});
const primitives=await fs.readFile(new URL('./app.part1.jsfrag',import.meta.url),'utf8');
vm.runInContext(primitives.slice(primitives.indexOf('function uiHint('),primitives.indexOf('const resourceHash')),context);
vm.runInContext(part4.slice(part4.indexOf('function providerIsEnabled('))+part5.slice(0,part5.indexOf('function requestFilters(')),context);
vm.runInContext(part12.slice(part12.indexOf('function codexUsageWindowLabel('),part12.indexOf('function clearCodexUsageResetState(')),context);
vm.runInContext(part12.slice(part12.indexOf('function codexAccountNeedsRelogin('),part12.indexOf('function codexAccountOperations(')),context);
vm.runInContext(part12.slice(part12.indexOf('function providerCodexUsageSection('),part12.indexOf('function codexProviderFormModal(')),context);
vm.runInContext(part14,context);

const cards=html=>html.match(/<article class="provider-codex-usage-window[\s\S]*?<\/article>/g)||[];
const noResetActions=html=>assert.doesNotMatch(html,/data-action="(?:codex-usage-reset|provider-codex-relogin)|class="codex-reset-credits/);
const kimiHTML=context.codingUsageView(kimiUsage),glmHTML=context.codingUsageView(glmUsage);
assert.equal(cards(kimiHTML).length,3);
assert.equal((kimiHTML.match(/role="meter"/g)||[]).length,3,'monthly Coding usage is a breakdown, not an independent quota');
assert.match(cards(kimiHTML)[2],/Monthly limit[\s\S]*Monthly Coding usage[\s\S]*5<span>% used/);
assert.match(cards(kimiHTML)[1],/Weekly limit[\s\S]*aria-valuenow="0"[\s\S]*Reset time unavailable/);
const resetLabel=new Date(1794193200*1000).toLocaleString('en-US',{year:'numeric',month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',hour12:false});
assert.ok(cards(kimiHTML)[0].includes('Resets '+esc(resetLabel)));
assert.doesNotMatch(kimiHTML,/>Used<|>Remaining<|>Total<|>requests<|>tokens</,'percentage-only windows have no invented quantities');
assert.match(glmHTML,/GLM Max/);
assert.match(cards(glmHTML)[1],/aria-valuenow="0"[\s\S]*>Used<\/span> <span>0<\/span> <span>credits/);
assert.match(cards(glmHTML)[2],/MCP calls limit[\s\S]*>5<\/span> <span>requests/);
assert.match(context.codingUsageView(zaiUsage),/Z\.ai Coding Plan/);
for(const missing of [undefined,null,'',false,NaN,Infinity,'0',-1]){
  const [card]=cards(context.codingUsageView({windows:[{label:'Unreported window',used_percent:missing,used:missing,remaining:missing,limit:missing}]}));
  assert.match(card,/Not reported/);
  assert.doesNotMatch(card,/role="meter"|% used/);
}
const [zeroLimit]=cards(context.codingUsageView({windows:[{used:0,limit:0,unit:'credits'}]}));
assert.match(zeroLimit,/>Used<\/span> <span>0<\/span> <span>credits/);
assert.doesNotMatch(zeroLimit,/role="meter"/);
const [inconsistent]=cards(context.codingUsageView({windows:[{remaining:101,limit:100}]}));
assert.match(inconsistent,/<strong>Not reported<\/strong>/);
assert.doesNotMatch(inconsistent,/role="meter"/,'remaining above the limit cannot imply zero usage');
assert.equal(context.codingUsagePercent({remaining:100,limit:100}),0);
assert.equal(context.codingUsagePercent({remaining:0,limit:100}),100);
assert.equal(context.codingUsagePercent({used_percent:0,used:100,limit:100}),0);
assert.match(context.codingUsageView(null),/No cached usage yet/);
assert.match(context.codingUsageView({windows:[]}),/No usage window reported/);
const unsafe='"><img src=x onerror=alert(1)><script>bad</script>';
const unsafeHTML=context.codingUsageView({...kimiUsage,plan_name:unsafe,windows:[{label:unsafe,used_percent:0,breakdown:[{label:unsafe,used_percent:0}]}]});
assert.match(unsafeHTML,/&lt;script&gt;/);
assert.doesNotMatch(unsafeHTML,/<script>|<img/);
context.consoleLocale='zh-CN';
for(const [label,zh] of [
  ['Coding Plan usage','编程套餐用量'],['5-hour limit','5 小时额度'],['Monthly limit','每月额度'],
  ['Monthly Coding usage','每月编程用量'],['MCP calls limit','MCP 调用额度'],['credits','积分'],
  ['Not reported','未提供'],['Used','已用'],['Remaining','剩余'],
])assert.equal(context.translatedConsoleText(label),zh);
context.consoleLocale='en-US';
console.log('PASS usage windows, genuine zero, missing quantities, reset times, safe labels and locale');

const detailData=p=>({provider:p,connections:p.connectionItems});
const entry=p=>context.providerCodexUsageEntry(p.id,p.connectionItems[0].id);
const list=p=>context.providerCodexUsagePills(p);
const detail=p=>context.providerSubscriptionUsageSection(p.id,p,detailData(p),p.connectionItems);
context.pageProviders();context.pageProviders();await tick();
for(const p of [kimi,glm,zai]){
  assert.deepEqual(calls.filter(([id])=>id===p.id).map(call=>call[2]),[false,true]);
  for(const html of [list(p),detail(p)]){
    assert.match(html,new RegExp(`data-action="provider-usage-refresh" data-provider="${p.id}" data-connection="${p.id}-key"`));
    noResetActions(html);
  }
}
assert.equal(calls.length,6,'ordinary API connections never request subscription usage');
assert.equal(list(ordinary),'');assert.match(detail(ordinary),/Quota query not supported/);
assert.equal(context.providerIsCodex({...kimi,logo:'codex'}),false);
entry(codex).usage=codexUsage;
assert.match(detail(codex),/Codex Usage[\s\S]*Usage reset credits/);

state.providerCache.set(glm.id,detailData(glm));
assert.match(context.pageProvider(glm.id),/Accounts & quota[\s\S]*GLM Max/);
await tick();
assert.deepEqual(calls.slice(6),[['glm','glm-key',true]],'detail shares the list snapshot and refreshes on entry');
await context.loadProviderCodexUsage(glm.id,detailData(glm));
await context.loadProviderListCodexUsage();
assert.equal(calls.length,7,'cached reads from either view do not refetch');

const refreshStart=part12.indexOf("  if(action==='provider-usage-refresh'){");
const refreshCode=part12.slice(refreshStart,part12.indexOf("  if(action==='provider-codex-usage'){",refreshStart));
let failRefresh,stopped=false,refreshCount=0;
adapter.getConnectionUsage=(id,connection,{refresh})=>{
  assert.deepEqual([id,connection,refresh],['kimi','kimi-key',true]);
  refreshCount++;
  return new Promise((_resolve,reject)=>{failRefresh=reject});
};
const clickRefresh=new Function('event','el','loadSharedCodexUsage',`const action='provider-usage-refresh';return (async()=>{${refreshCode}})()`);
const refreshing=clickRefresh({stopImmediatePropagation(){stopped=true}},{dataset:{provider:kimi.id,connection:'kimi-key'}},context.loadSharedCodexUsage);
await tick();
state.providerCache.set(kimi.id,detailData(kimi));
context.pageProvider(kimi.id);await tick();
assert.equal(stopped,true);assert.equal(refreshCount,1,'navigation joins the explicit in-flight refresh');
for(const html of [list(kimi),detail(kimi)])assert.match(html,/aria-busy="true"[\s\S]*aria-valuenow="25"/);
failRefresh(new Error('quota upstream unavailable'));await refreshing;
for(const html of [list(kimi),detail(kimi)]){
  assert.match(html,/quota upstream unavailable[\s\S]*(?:Showing the last cached snapshot|Cached snapshot)/);
  assert.match(html,/aria-valuenow="25"/);
  noResetActions(html);
}
console.log('PASS shared list/detail queries, explicit refresh, Codex controls and failed-refresh snapshot retention');

adapter.getConnectionUsage=getUsage;
context.pageProviders();await tick();
const saveStart=part12.indexOf("  if(action==='provider-create'){");
const saveCode=part12.slice(saveStart,part12.indexOf('\n},true);',saveStart));
Object.assign(context,{
  event:{stopImmediatePropagation(){}},validateProviderEditor:()=>'',providerEditorPayload:w=>w,
  closeOverlay(){},showToast(){},render:()=>context.pageProviders(),refreshProviderResources:async()=>context.pageProviders(),
});
state.wizard={providerEdit:{type:'custom'}};
const runProviderAction=async(action,id)=>{
  Object.assign(context,{action,el:{dataset:{id}}});
  await vm.runInContext(`(async()=>{${saveCode}})()`,context);await tick();
};
let before=calls.length;
const original=entry(kimi);
adapter.saveProviderProtocolConfig=async()=>{throw new Error('save rejected')};
await runProviderAction('provider-save',kimi.id);
assert.equal(entry(kimi),original);assert.equal(calls.length,before);
adapter.saveProviderProtocolConfig=async()=>{};
snapshots.set(kimi.id,{...kimiUsage,plan_name:'Replacement key'});
await runProviderAction('provider-save',kimi.id);
assert.deepEqual(calls.slice(before),[['kimi','kimi-key',false],['kimi','kimi-key',true]]);
assert.notEqual(entry(kimi),original);assert.match(list(kimi),/Replacement key/);
const added=provider('added','https://api.z.ai/api/coding/paas/v4');
adapter.addProvider=async()=>added;adapter.getProviders=async()=>[...state.providers,added];
snapshots.set(added.id,zaiUsage);
state.wizard.provider=added;before=calls.length;
await runProviderAction('provider-create');
assert.deepEqual(calls.slice(before),[['added','added-key',false],['added','added-key',true]]);
state.providers.reverse();before=calls.length;
context.pageProviders();await tick();
assert.equal(calls.length,before,'reordering the same accounts does not query upstream again');
console.log('PASS successful saves and new accounts refresh on the same view; failed saves retain cache');

const json=(body,status=200)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
const requests=[];
const http=new HttpSmartAPIAdapter('',async(path,options)=>{
  requests.push([path,options.method||'GET']);
  return json({provider_id:'provider/a',connection_id:'key/b',usage:path.includes('refresh=false')?null:kimiUsage});
});
assert.deepEqual(await http.getConnectionUsage('provider/a','key/b'),kimiUsage);
assert.deepEqual(await http.getConnectionUsage('provider/a','key/b',{refresh:true}),kimiUsage);
assert.equal(await http.getConnectionUsage('provider/a','key/b',{refresh:false}),null);
await http.getConnectionOAuthUsage('provider/a','key/b',{refresh:false});
assert.deepEqual(requests,[
  ['/api/providers/provider%2Fa/connections/key%2Fb/usage','GET'],
  ['/api/providers/provider%2Fa/connections/key%2Fb/usage','GET'],
  ['/api/providers/provider%2Fa/connections/key%2Fb/usage?refresh=false','GET'],
  ['/api/providers/provider%2Fa/connections/key%2Fb/oauth/usage?refresh=false','GET'],
]);
const unavailable=new HttpSmartAPIAdapter('',async()=>json({error:{message:'quota unavailable'}},503));
await assert.rejects(unavailable.getConnectionUsage('p','c'),/quota unavailable/);
class LegacyAdapter extends SmartAPIAdapter{async getConnectionOAuthUsage(){return codexUsage}}
assert.deepEqual(await new LegacyAdapter().getConnectionUsage('p','c'),codexUsage);

const demo=new MockSmartAPIAdapter();
for(const [p,usage] of [[kimi,kimiUsage],[glm,glmUsage],[zai,zaiUsage],[ordinary,null]]){
  demo.data.providers.push(p);demo.data.connections[p.id]=p.connectionItems;
  const [connection]=p.connectionItems;
  assert.equal(await demo.getConnectionUsage(p.id,connection.id,{refresh:false}),null);
  const fresh=await demo.getConnectionUsage(p.id,connection.id);
  if(!usage){assert.equal(fresh,null);continue;}
  assert.equal(fresh.provider,usage.provider);assert.equal(fresh.windows.length,3);
  fresh.windows.length=0;
  assert.equal((await demo.getConnectionUsage(p.id,connection.id,{refresh:false})).windows.length,3,'demo cache returns a copy');
  demo.data.connections[p.id]=[{...connection}];
  assert.equal(await demo.getConnectionUsage(p.id,connection.id,{refresh:false}),null,'replacement connection has no cached usage');
}
assert.equal((await demo.getConnectionUsage('codex-personal','account-a')).plan_type,'pro');
console.log('PASS shared HTTP contract, demo cache and legacy Codex adapter compatibility');
