// One in-flight load per observation view. Generations also guard adapters that
// cannot honor AbortSignal (including a delayed standalone fixture response).
export class ObservationResource {
  constructor(onChange=()=>{}) {
    this.onChange=onChange;this.generation=0;this.loading=false;this.data=null;this.error=null;this.promise=null;this.controller=null;
  }
  load(read,{replace=false}={}) {
    if(this.promise&&!replace)return this.promise;
    this.controller?.abort();const generation=++this.generation,controller=new AbortController();
    this.controller=controller;this.loading=true;this.error=null;
    const pending=Promise.resolve().then(()=>read(controller.signal)).then(data=>{
      if(generation!==this.generation||controller.signal.aborted)return;
      this.data=data;this.onChange('success',this);return data;
    }).catch(error=>{
      if(generation!==this.generation||controller.signal.aborted||error?.name==='AbortError')return;
      this.error=error;this.onChange('error',this);
    }).finally(()=>{
      if(generation!==this.generation)return;
      this.loading=false;this.promise=null;this.onChange('settled',this);
    });
    this.promise=pending;this.onChange('loading',this);return pending;
  }
  cancel() {
    this.generation++;this.controller?.abort();this.controller=null;this.promise=null;this.loading=false;
  }
}
