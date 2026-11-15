import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {MockSmartAPIAdapter} from './api.js';
import {scenarios} from './mock-data.js';
import * as observabilityModel from './observability-model.js';
import {ObservationResource} from './observability-state.js';
// Render the complete assembled application in one lexical scope. This checks
// renderer composition, not browser layout (which needs browser acceptance).
const node=()=>({dataset:{},classList:{add(){},remove(){},contains(){return false},toggle(){}},addEventListener(){},querySelector(){return null},querySelectorAll(){return []},contains(){return false},setAttribute(){},append(){},remove(){},innerHTML:''});
const app=node(),portal=node(),root=node();
const adapter=new MockSmartAPIAdapter();
const location={hash:'#/overview',href:'https://console.test/#/overview'};
const document={documentElement:root,body:node(),querySelector:s=>s==='#app'?app:s==='#portal-root'?portal:null,querySelectorAll:()=>[],addEventListener(){},createElement:()=>node(),createTreeWalker:()=>({nextNode:()=>false}),activeElement:null};
const context=vm.createContext({console,URL,URLSearchParams,Date,Intl,AbortController,Blob,Map,Set,Response,NodeFilter:{SHOW_TEXT:4},document,location,
 createAdapter:()=>adapter,scenarios,observabilityModel,ObservationResource,mountRouteExplanation(){},installActionMenus(){},installTooltips(){},
 window:{addEventListener(){},matchMedia:()=>({matches:false,addEventListener(){}})},matchMedia:()=>({matches:false,addEventListener(){}}),navigator:{language:'en-US'},localStorage:{getItem(){return null},setItem(){}},
 history:{replaceState(_a,_b,url){location.hash=url.startsWith('#')?url:new URL(url).hash}},
 setInterval(){},clearInterval(){},setTimeout(){},clearTimeout(){},queueMicrotask(){},requestAnimationFrame(){},confirm:()=>true,
});
const parts=await Promise.all(Array.from({length:15},(_,i)=>fs.readFile(new URL(`./app.part${i+1}.jsfrag`,import.meta.url),'utf8')));
const source=parts.join('').replace(/^import .*;\n/gm,'').replace(/^boot\(\)\.then\(\(\)=>pollCodexUsageResets\(\)\);$/m,'');
vm.runInContext(source,context);
const run=code=>vm.runInContext(code,context);
context.fixture={auth:{initialized:true,authenticated:true},loading:false,partialErrors:[],models:await adapter.getModels(),providers:await adapter.getProviders(),requests:await adapter.getRequests(),apiKeys:await adapter.getApiKeys(),settings:await adapter.getSettings(),revisions:await adapter.getRevisions(),managementTokens:[],securityEvents:[],overview:await adapter.getOverview(),billing:await adapter.getBilling()};
run('Object.assign(state,fixture)');
for(const provider of context.fixture.providers){context.detail=await adapter.getProvider(provider.id);run('state.providerCache.set(detail.provider.id,detail)')}
for(const model of context.fixture.models){context.detail=await adapter.getModel(model.id);run('state.modelCache.set(detail.model.id,detail)')}
for(const request of context.fixture.requests.slice(0,2)){context.detail=await adapter.getRequest(request.id);run('state.requestCache.set(detail.id,detail)')}
let count=0;
for(const locale of ['en-US','zh-CN']){
 context.locale=locale;run('consoleLocale=locale');
 const routes=['overview','models','providers','queue','requests','sessions','billing','api-keys',...['general','upstream','concurrency','import','backup','revisions','security'].map(x=>'settings/'+x),...context.fixture.providers.map(p=>'providers/'+encodeURIComponent(p.id)),...context.fixture.models.map(m=>'models/'+encodeURIComponent(m.id)),...context.fixture.requests.slice(0,2).map(r=>'requests/'+r.id)];
 for(const route of routes){location.hash='#/'+route;run('render()');const html=app.innerHTML;assert.equal(typeof html,'string',route);assert.doesNotMatch(html,/NaN|\$\{/,route+' contains an unrendered expression');assert.ok(html.length>20,route);if(route.startsWith('requests/'))assert.doesNotMatch(html,/<details\b(?![^>]*\bopen\b)[^>]*>/,'Request history details start expanded');count++}
 const provider=context.fixture.providers[0];location.hash='#/providers/'+provider.id+'?tab=configuration';assert.match(run('renderPage(currentRoute())'),/<table/);
 // Compose the actual form helpers as well as pages, including nested tooltips.
 const forms=[
  'providerEditorModal(state.wizard.provider=newProviderEditor(),{type:"add-provider"})',
  'providerModelSyncCatalogSettings({...newProviderEditor(),modelSyncCatalogMode:"volcengine-plan"})',
  'createManagementTokenModal()',
  'modelFormModal({mode:"create"})',
  'simulationModal({modelId:"fixture"})',
  'settingsCodexVersion()',
  'settingsAutomaticUsageReset()',
 ];
 for(const expression of forms){const html=run(expression);assert.doesNotMatch(html,/NaN|\$\{/,expression+' contains an unrendered expression');assert.ok(html.length>50,expression);}

 for(const name of ['models','protocols']){context.scopeField=name;run('state.wizard.keyPolicy={models:"retired-model",protocols:"",name:"Fixture",expiresAt:""}');assert.match(run('keyScopePicker("keyPolicy",scopeField,state.wizard.keyPolicy[scopeField])'),/data-scope-option/)}
}
// Each independent account remains a row, even when all limits are identical.
context.accountFixture=structuredClone(context.fixture.providers[0]);
run('state.providers=[accountFixture];state.providerCache.clear();state.providerCodexUsage.clear();consoleLocale="en-US"');
const accounts=run('providerUsageGroups(accountFixture)');assert.equal(accounts.length,4);
run('for(const [i,g] of providerUsageGroups(accountFixture).entries()){providerCodexUsageEntry(accountFixture.id,g.connection.id).usage={windows:[{label:"5-hour",used_percent:[0,25,55,90][i]},{label:"Weekly",used_percent:12}]}}');
const independentHTML=run('providerTableRow(accountFixture,"Healthy")');
assert.equal((independentHTML.match(/<tr /g)||[]).length,4);
for(const value of [0,25,55,90])assert.match(independentHTML,new RegExp('aria-valuenow="'+value+'"'));
assert.equal((independentHTML.match(/data-action="provider-usage-refresh"/g)||[]).length,4);
assert.equal((independentHTML.match(/class="provider-identity-cell"/g)||[]).length,1);
run('for(const c of accountFixture.connectionItems)c.quotaGroupId="same-account";state.providerCodexUsage.clear()');
assert.equal((run('providerTableRow(accountFixture,"Healthy")').match(/<tr /g)||[]).length,1,'shared account protocols are one row');
console.log('PASS independent quota accounts remain separate rows and shared protocols collapse once');
// Coding providers use the same account hierarchy, with every reported window.
// Four accounts x three protocols must remain four rows, never twelve or one.
for(const [id,base,paths,labels] of [
 ['k3','https://api.kimi.com',['/coding/v1','/coding/v1/responses','/coding/v1/messages'],['5-hour limit','Weekly limit','Monthly limit']],
 ['glm','https://open.bigmodel.cn',['/api/coding/paas/v4','/api/v1','/api/anthropic/v1'],['Coding limit','Credits limit','MCP calls limit']],
 ['zai','https://api.z.ai',['/api/coding/paas/v4','/api/v1','/api/anthropic/v1'],['Coding limit','Credits limit','MCP calls limit']],
]){
 context.codingFixture={id,name:id,raw:{type:'custom',base_url:base+paths[0]},connectionItems:Array.from({length:4},(_,account)=>paths.map((path,protocol)=>({
  id:`${id}-${account}-${protocol}`,name:`${id}-account-${account+1}`,quotaGroupId:`${id}-quota-${account}`,status:account===0?'Recovering':'Healthy',
  baseUrl:base+path,models:[{protocols:[['openai-chat','openai-responses','anthropic'][protocol]]}],
 }))).flat()};
 context.codingLabels=labels;
 run('state.providers=[codingFixture];state.providerCache.clear();state.providerCodexUsage.clear()');
 run('for(const [i,g] of providerUsageGroups(codingFixture).entries()){providerCodexUsageEntry(codingFixture.id,g.connection.id).usage={windows:codingLabels.map((label,j)=>({label,used_percent:j===2?90:i===3?null:0}))}}');
 const groups=run('providerUsageGroups(codingFixture)');assert.equal(groups.length,4,id);
 assert.ok(groups.every(g=>g.members.length===3&&g.protocols.length===3),id+' protocols stay attached to their account');
 const html=run('providerTableRow(codingFixture,"Recovering")'),rows=html.match(/<tr\b[\s\S]*?<\/tr>/g);
 assert.equal(rows.length,4,id);assert.match(html,/rowspan="4"/);assert.match(html,/4 accounts/);
 for(const [i,row] of rows.entries()){
  assert.equal((row.match(/role="meter"/g)||[]).length,3,id+' retains all windows');
  for(const label of labels)assert.ok(row.includes(label),id+' '+label);
  assert.match(row,new RegExp(`data-provider="${id}" data-connection="${id}-${i}-0"`));
  assert.equal((row.match(/data-action="provider-usage-refresh"/g)||[]).length,1);
  assert.match(row,i===3?/aria-valuetext="Unknown"/:/aria-valuenow="0"/);
  assert.match(row,/aria-valuenow="90"/,'third window must remain visible');
 }
 // A failed refresh is local to its account and retains that account's snapshot.
 run('providerCodexUsageEntry(codingFixture.id,codingFixture.connectionItems[3].id).error="fixture refresh failed"');
 const failedRows=run('providerTableRow(codingFixture,"Recovering")').match(/<tr\b[\s\S]*?<\/tr>/g);
 assert.match(failedRows[1],/Refresh failed · previous snapshot/);
 assert.ok(failedRows.filter((_,i)=>i!==1).every(row=>!row.includes('Refresh failed')));
 // Without verified membership, equal labels and percentages never merge accounts.
 run('for(const c of codingFixture.connectionItems)delete c.quotaGroupId');
 assert.equal(run('providerUsageGroups(codingFixture).length'),12,id+' unconfirmed connections remain independent');
}
console.log('PASS K3, GLM and Z.ai: four independent accounts, shared protocols, all quota windows and isolated failures');
// Same credential and protocol group appears once, with separate resources intact.
run('state.providers=[{id:"p",raw:{type:"custom",base_url:"https://open.bigmodel.cn/api/coding/paas/v4"},connectionItems:["openai-chat","openai-responses","anthropic"].map(id=>({id,quotaGroupId:"shared",models:[{protocols:[id]}]}))}]');
assert.equal(run('providerUsageGroups(state.providers[0]).length'),1);
// URL snapshots include filters/pagination and reject external return destinations.
location.hash='#/requests';run('Object.assign(state,{requestSearch:"customer <A>",requestCallerIdentity:"caller-id",requestOffset:50,requestLimit:25});persistRequestView()');
const saved=location.hash;run('state.appliedRequestView="";state.requestSearch="";state.requestOffset=0');assert.equal(run('applyRequestViewLocation()'),true);assert.equal(run('state.requestSearch'),'customer <A>');assert.equal(run('state.requestOffset'),50);
location.hash='#/requests/r?back='+encodeURIComponent(saved);assert.equal(run('resourceReturnHash()'),saved);
location.hash='#/requests/r?back='+encodeURIComponent('https://outside.test');assert.equal(run('resourceReturnHash()'),'#/requests');
console.log(`PASS ${count} complete page renderers in both locales, account grouping and navigation context`);

// Unrelated audit reads may stay pending without holding the initial page.
let releaseAudit;
adapter.getSecurityEvents=()=>new Promise(resolve=>releaseAudit=resolve);
location.hash='#/providers';context.rendered=0;run('render=()=>{rendered++};state.auth={initialized:true,authenticated:true}');
const booting=run('boot()');
const deadline=Date.now()+3000;
while(run('state.loading')&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,10));
assert.equal(run('state.loading'),false);assert.ok(context.rendered>0);assert.equal(typeof releaseAudit,'function');
releaseAudit([]);await booting;
console.log('PASS initial page becomes available before unrelated audit/security reads');

// Expiry removes one-time secrets immediately and leaves the route recoverable.
const beforeExpiry=location.hash;portal.innerHTML='synthetic-secret';run('state.modal={type:"key-reveal",key:"synthetic-secret"};state.wizard={key:{secret:"synthetic-secret"}};expireConsoleSession()');
assert.equal(portal.innerHTML,'');assert.equal(run('state.modal'),null);assert.equal(run('state.wizard.key'),undefined);assert.equal(run('state.auth.authenticated'),false);assert.equal(location.hash,beforeExpiry);
// An older auth-status response cannot undo a later completed boot.
const authReplies=[];adapter.getAuthStatus=()=>new Promise(resolve=>authReplies.push(resolve));adapter.getSecurityEvents=async()=>[];
const oldBoot=run('boot()'),newBoot=run('boot()');authReplies[1]({initialized:true,authenticated:true,username:'admin'});await newBoot;authReplies[0]({initialized:true,authenticated:false});await oldBoot;
assert.equal(run('state.auth.authenticated'),true);
console.log('PASS session expiry clears secrets and stale boot responses cannot replace current authentication');
