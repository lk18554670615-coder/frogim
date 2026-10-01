import { useEffect, useRef, type ReactNode } from 'react';

export function PlatformDialog({title,children,onClose,drawer=false,footer}:{title:string;children:ReactNode;onClose:()=>void;drawer?:boolean;footer?:ReactNode}) {
  const panel=useRef<HTMLDivElement>(null);
  const close=useRef(onClose);
  close.current=onClose;
  useEffect(()=>{
    const previous=document.activeElement as HTMLElement|null;
    const overflow=document.body.style.overflow;
    const rootOverflow=document.documentElement.style.overflow;
    document.body.style.overflow='hidden';
    document.documentElement.style.overflow='hidden';
    panel.current?.focus();
    const key=(e:KeyboardEvent)=>{
      if(e.key==='Escape')close.current();
      if(e.key!=='Tab')return;
      const els=Array.from(panel.current?.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href],[tabindex="0"]')??[]);
      if(!els.length){e.preventDefault();return;}
      const first=els[0],last=els[els.length-1];
      if(e.shiftKey&&(document.activeElement===first||document.activeElement===panel.current)){e.preventDefault();last.focus();}
      else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}
    };
    document.addEventListener('keydown',key);
    return()=>{
      document.body.style.overflow=overflow;
      document.documentElement.style.overflow=rootOverflow;
      document.removeEventListener('keydown',key);
      previous?.focus();
    };
  },[]);
  return <div className="lp-shade" onMouseDown={e=>{if(e.target===e.currentTarget)close.current();}}>
    <div ref={panel} tabIndex={-1} role="dialog" aria-modal="true" aria-label={title} className={drawer?'lp-dialog lp-drawer':'lp-dialog'}>
      <header><h2>{title}</h2><button type="button" aria-label="关闭" onClick={()=>close.current()}>×</button></header>
      <div className="lp-dialog-body">{children}</div>
      {footer&&<footer className="lp-dialog-footer">{footer}</footer>}
    </div>
  </div>;
}
