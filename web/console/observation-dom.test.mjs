import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';

// A small DOM fixture checks node identity across refreshes; it does not claim
// browser layout coverage. Detaching the mounted graph is the scroll regression.
let protectedGraph=null,graphDetachments=0;
class Node {
  constructor(name,attrs={},children=[]){
    this.nodeName=name;this.nodeType=name==='#text'?3:1;this.nodeValue=name==='#text'?attrs:null;
    this.attrs=new Map(this.nodeType===1?Object.entries(attrs):[]);this.childNodes=[];this.parentNode=null;
    this.classList={contains:value=>(this.getAttribute('class')||'').split(' ').includes(value)};
    this.scrollLeft=0;this.open=false;this._value=attrs.value||'';
    for(const child of children)this.insertBefore(child,null);
  }
  get attributes(){return [...this.attrs].map(([name,value])=>({name,value}))}
  getAttribute(name){return this.attrs.get(name)??null}
  hasAttribute(name){return this.attrs.has(name)}
  setAttribute(name,value){this.attrs.set(name,value)}
  removeAttribute(name){this.attrs.delete(name)}
  get value(){return this.nodeName==='SELECT'?this.childNodes.find(n=>n.hasAttribute('selected'))?.getAttribute('value')||this.childNodes[0]?.getAttribute('value')||'':this._value}
  set value(value){this._value=value;if(this.nodeName==='SELECT')for(const n of this.childNodes){if(n.getAttribute('value')===value)n.setAttribute('selected','');else n.removeAttribute('selected')}}
  contains(node){return this===node||this.childNodes.some(n=>n.contains(node))}
  remove(){
    if(!this.parentNode)return;
    if(protectedGraph&&this.contains(protectedGraph))graphDetachments++;
    this.parentNode.childNodes.splice(this.parentNode.childNodes.indexOf(this),1);this.parentNode=null;
  }
  insertBefore(node,reference){node.remove();const i=reference?this.childNodes.indexOf(reference):this.childNodes.length;assert.ok(i>=0);this.childNodes.splice(i,0,node);node.parentNode=this;return node}
  replaceWith(node){const parent=this.parentNode;parent.insertBefore(node,this);this.remove()}
  focus(){throw new Error('A retained focused control must not be refocused')}
  setSelectionRange(start,end){this.selectionStart=start;this.selectionEnd=end}
}
const el=(name,attrs={},children=[])=>new Node(name,attrs,children),text=value=>new Node('#text',value);
const row=(id,count)=>el('TR',{'data-observation-row':id},[el('TD',{},[text(String(count))])]);
const input=()=>el('INPUT',{'data-observe-field':'q',value:'selected'});
const table=rows=>el('DIV',{class:'table-wrap'},[el('TABLE',{},[el('TBODY',{},rows)])]);
const graph=()=>el('DIV',{'data-observation-graph':'sessions:'});
const panel=g=>el('SECTION',{class:'observability-section observation-focus-panel'},[g]);
const mountedGraph=graph(),disclosure=el('DETAILS',{},[text('Mounted graph contents')]);disclosure.open=true;mountedGraph.insertBefore(disclosure,null);
const search=input(),oldRow=row('s1',1),scroll=table([oldRow]);scroll.scrollLeft=87;
const live=el('DIV',{class:'observability-page'},[search,scroll,panel(mountedGraph)]);
protectedGraph=mountedGraph;
let nextPage,updates=0;
const context=vm.createContext({Array,document:{activeElement:search,createElement:()=>({content:{get firstElementChild(){return nextPage}}})},
  app:{querySelector:selector=>selector==='.observability-page'?live:selector==='[data-observe-field="q"]'?search:null},
  activeObservationContext:{type:'sessions',id:''},pageSessions:()=>'<div></div>',localizeDOM(){},
  syncObservabilityView(){updates++},currentRoute:()=>({section:'sessions'}),render(){assert.fail('Session polling must not rebuild the app shell')},
});
search.dataset={observeField:'q'};search.selectionStart=2;search.selectionEnd=5;
const source=await fs.readFile(new URL('./app.part15.jsfrag',import.meta.url),'utf8');
vm.runInContext(source.slice(source.indexOf('function observationNodeKey('),source.indexOf('function observationFilters(')),context);
const render=()=>vm.runInContext('renderObservationPreservingFocus()',context);
for(let n=2;n<=4;n++){
  nextPage=el('DIV',{class:'observability-page'},[input(),table([row('s0',n),row('s1',n)]),panel(graph())]);
  render();
  assert.equal(live.childNodes[0],search);assert.equal(live.childNodes[1],scroll);assert.equal(scroll.scrollLeft,87);
  assert.equal(scroll.childNodes[0].childNodes[0].childNodes[1],oldRow);
  assert.equal(oldRow.childNodes[0].childNodes[0].nodeValue,String(n));
  assert.equal(live.childNodes[2].childNodes[0],mountedGraph);assert.equal(disclosure.open,true);
  assert.deepEqual([search.selectionStart,search.selectionEnd],[2,5]);
}
assert.equal(graphDetachments,0);assert.equal(updates,3);
nextPage=el('DIV',{class:'observability-page'},[input(),table([row('s1',5)]),panel(graph())]);render();
assert.equal(scroll.childNodes[0].childNodes[0].childNodes.length,1);
assert.equal(scroll.childNodes[0].childNodes[0].childNodes[0],oldRow);
assert.equal(graphDetachments,0);

// New option nodes move out of the template; snapshot the selected value before
// moving them, otherwise an empty template select can clear the live selection.
const selected=el('SELECT',{'data-observe-field':'request'},[el('OPTION',{value:'r1'},[text('r1')])]);
context.selected=selected;context.replacement=el('SELECT',{'data-observe-field':'request'},[el('OPTION',{value:'r2',selected:''},[text('r2')])]);
vm.runInContext('patchObservationNode(selected,replacement)',context);assert.equal(selected.value,'r2');
console.log('PASS Session refresh preserves graph, disclosures, focused search, keyed rows and table scroll; updates values in place');
