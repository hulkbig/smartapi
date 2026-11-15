// One body-level tooltip avoids clipping inside scrollable tables and dialogs.
// Help opens on touch; ordinary action buttons keep their original click action.
export function installTooltips(doc=document,win=window,translate=value=>value){
  const bubble=doc.createElement('div');
  bubble.id='console-tooltip';bubble.className='console-tooltip';
  bubble.setAttribute('role','tooltip');bubble.hidden=true;doc.body.append(bubble);
  let owner=null,timer=null,pinned=false,dismissed=null;
  const hide=()=>{
    clearTimeout(timer);if(owner){
      const ids=(owner.getAttribute('aria-describedby')||'').split(/\s+/).filter(id=>id&&id!==bubble.id);
      if(ids.length)owner.setAttribute('aria-describedby',ids.join(' '));else owner.removeAttribute('aria-describedby');
    }
    owner=null;pinned=false;bubble.hidden=true;
  };
  const place=()=>{
    if(!owner?.isConnected){hide();return}
    const anchor=owner.getBoundingClientRect(),box=bubble.getBoundingClientRect();
    const left=Math.max(8,Math.min(win.innerWidth-box.width-8,anchor.left+(anchor.width-box.width)/2));
    const below=anchor.bottom+8,top=below+box.height<win.innerHeight-8?below:Math.max(8,anchor.top-box.height-8);
    bubble.style.left=`${left}px`;bubble.style.top=`${top}px`;
  };
  const show=target=>{
    clearTimeout(timer);if(!target||target===dismissed)return;
    if(owner!==target)hide();owner=target;
    const text=target.dataset.tooltip||target.getAttribute('aria-label')||'';
    bubble.textContent=target.hasAttribute('data-tooltip-literal')?text:translate(text);bubble.hidden=false;
    const ids=new Set((target.getAttribute('aria-describedby')||'').split(/\s+/).filter(Boolean));ids.add(bubble.id);
    target.setAttribute('aria-describedby',[...ids].join(' '));place();
  };
  const leave=()=>{clearTimeout(timer);timer=setTimeout(()=>{if(!pinned&&!bubble.matches(':hover')&&!owner?.matches(':hover')&&doc.activeElement!==owner)hide()},140)};
  const targetOf=event=>event.target?.closest?.('[data-tooltip]');
  doc.addEventListener('pointerover',event=>{const target=targetOf(event);if(target)show(target);else if(event.target===bubble)clearTimeout(timer)});
  doc.addEventListener('pointerout',event=>{if(targetOf(event)===dismissed)dismissed=null;leave()});
  doc.addEventListener('focusin',event=>{dismissed=null;const target=targetOf(event);if(target)show(target);else hide()});
  doc.addEventListener('focusout',leave);
  doc.addEventListener('click',event=>{
    const target=targetOf(event);
    if(target?.hasAttribute('data-help')){event.preventDefault();event.stopImmediatePropagation();if(owner===target&&pinned)hide();else{dismissed=null;show(target);pinned=true}}
    else if(event.target!==bubble)hide();
  },true);
  doc.addEventListener('keydown',event=>{if(event.key==='Escape'&&owner){dismissed=owner;hide();event.stopImmediatePropagation()}},true);
  doc.addEventListener('scroll',hide,true);win.addEventListener('resize',hide);win.addEventListener('hashchange',hide);
  return {show,hide,element:bubble};
}

// Keep menus in their original DOM ancestry so delegated actions still work.
// The top layer escapes table clipping; fixed positioning is the fallback.
export function installActionMenus(doc=document,win=window){
  let active=null;
  const close=(restoreFocus=false)=>{
    if(!active)return;
    const {menu,panel,trigger}=active;active=null;
    if(typeof panel.hidePopover==='function'&&panel.matches(':popover-open'))panel.hidePopover();
    menu.open=false;trigger.setAttribute('aria-expanded','false');
    if(restoreFocus&&trigger.isConnected)trigger.focus({preventScroll:true});
  };
  const place=()=>{
    if(!active)return;
    const {menu,panel,trigger}=active;
    if(!menu.isConnected){close();return;}
    const anchor=trigger.getBoundingClientRect(),box=panel.getBoundingClientRect();
    const left=Math.max(8,Math.min(win.innerWidth-box.width-8,anchor.right-box.width));
    const below=anchor.bottom+6;
    const top=below+box.height<=win.innerHeight-8?below:Math.max(8,anchor.top-box.height-6);
    panel.style.left=`${left}px`;panel.style.top=`${top}px`;
  };
  doc.addEventListener('toggle',event=>{
    const menu=event.target;
    if(!menu.matches?.('.v3-more-menu'))return;
    if(!menu.open){if(active?.menu===menu)close();return;}
    if(active?.menu!==menu)close();
    const trigger=menu.querySelector('summary'),panel=menu.querySelector('.v3-more-popover');
    if(!trigger||!panel)return;
    active={menu,trigger,panel};trigger.setAttribute('aria-expanded','true');
    if(typeof panel.showPopover==='function'){
      panel.setAttribute('popover','manual');
      if(!panel.matches(':popover-open'))panel.showPopover();
    }
    place();
  },true);
  doc.addEventListener('click',event=>{
    if(active&&(!active.menu.contains(event.target)||event.target.closest('button,a[href]')))close();
  });
  doc.addEventListener('focusin',event=>{if(active&&!active.menu.contains(event.target))close();});
  doc.addEventListener('keydown',event=>{
    if(event.key==='Escape'&&active){event.preventDefault();event.stopImmediatePropagation();close(true);}
  },true);
  doc.addEventListener('scroll',event=>{if(active&&!active.panel.contains(event.target))close();},true);
  win.addEventListener('resize',()=>close());win.addEventListener('hashchange',()=>close());
  return {close};
}
