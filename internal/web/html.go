package web

import (
	_ "embed"
	"strings"
)

//go:embed alpine.min.js
var alpineJS string

func buildIndexHTML() string {
	return strings.Replace(indexHTMLTemplate, "<!--ALPINE_JS-->", "<script>"+alpineJS+"</script>", 1)
}

const indexHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Doomsday</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#1a1816;--bg2:#222019;--bg3:#2e2b28;--bg4:#3a3633;
  --fg:#e8e0d8;--fg2:#a89f91;--fg3:#6b6560;
  --brand:#ff6b35;--brand2:#d4a574;
  --green:#4ade80;--red:#f87171;--yellow:#fbbf24;
  --radius:6px;
  --mono:'SF Mono','Fira Code','Cascadia Code',Consolas,monospace;
  --sans:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
}
html{font-size:13px}
body{background:var(--bg);color:var(--fg);font-family:var(--sans);line-height:1.4}
a{color:var(--brand);text-decoration:none;cursor:pointer}
a:hover{text-decoration:underline}
.app{display:flex;flex-direction:column;height:100vh;overflow:hidden}
.hdr{background:var(--bg2);border-bottom:1px solid var(--bg4);padding:0 16px;display:flex;align-items:center;height:38px;flex-shrink:0;gap:8px}
.hdr .logo{color:var(--brand);font-weight:700;font-size:14px;letter-spacing:.5px;white-space:nowrap;margin-right:4px}
.hdr .sep{color:var(--bg4);margin:0 2px}
.crumbs{display:flex;align-items:center;gap:0;font-size:12px;flex:1;overflow:hidden;min-width:0}
.crumbs a,.crumbs span.leaf{white-space:nowrap;padding:0 1px}
.crumbs a{color:var(--fg3)}.crumbs a:hover{color:var(--brand);text-decoration:none}
.crumbs span.leaf{color:var(--fg);font-weight:600}
.crumbs .sl{color:var(--fg3);margin:0 3px;flex-shrink:0}
.actions{display:flex;align-items:center;gap:6px;flex-shrink:0}
.btn{display:inline-flex;align-items:center;gap:4px;padding:3px 10px;border-radius:var(--radius);border:1px solid var(--bg4);background:var(--bg3);color:var(--fg);font-size:11px;cursor:pointer;transition:all .1s;font-family:var(--sans);white-space:nowrap}
.btn:hover{background:var(--bg4);border-color:var(--fg3)}
.btn.primary{background:var(--brand);border-color:var(--brand);color:#fff}
.btn.primary:hover{opacity:.85}
.btn:disabled{opacity:.4;cursor:default}
.finput{padding:4px 8px;background:var(--bg3);border:1px solid var(--bg4);border-radius:var(--radius);color:var(--fg);font-family:var(--mono);font-size:12px;outline:none}
.finput:focus{border-color:var(--brand)}.finput::placeholder{color:var(--fg3)}
.main{flex:1;overflow-y:auto}
table.dt{width:100%;border-collapse:collapse}
table.dt th{text-align:left;padding:5px 14px;font-size:10px;text-transform:uppercase;letter-spacing:.6px;color:var(--fg3);border-bottom:2px solid var(--bg4);font-weight:600;position:sticky;top:0;background:var(--bg);z-index:1}
table.dt td{padding:4px 14px;border-bottom:1px solid var(--bg3);font-size:13px;white-space:nowrap}
table.dt tbody tr{cursor:pointer;transition:background .06s}
table.dt tbody tr:hover{background:var(--bg2)}
table.dt .r{text-align:right}
table.dt .m{font-family:var(--mono);font-size:12px;color:var(--fg2)}
table.dt .d{color:var(--fg3)}
table.dt .b{color:var(--brand)}
table.dt .n{font-weight:500}
table.dt .dir{color:var(--brand2)}
.dot{display:inline-block;width:7px;height:7px;border-radius:50%;margin-right:6px}
.dot.g{background:var(--green)}.dot.y{background:var(--yellow)}.dot.rd{background:var(--red)}
.ic{width:22px;text-align:center;color:var(--fg3)}
.main pre{padding:0;margin:0;font-family:var(--mono);font-size:12px;line-height:1.5;overflow-x:auto;background:var(--bg);min-height:100%}
.main pre .chroma{display:block;padding:10px 14px;min-height:100%}
.main pre .chroma{background:transparent !important}
.main pre .lnt{color:var(--fg3);margin-right:10px;user-select:none}
.main img.preview{max-width:100%;height:auto;display:block;margin:20px auto}
.main iframe.preview{width:100%;height:100%;border:none}
.main .binary-msg{padding:60px 20px;text-align:center;color:var(--fg3)}
.main .binary-msg .sz{font-size:16px;color:var(--fg2);margin:8px 0}
.modal-bg{position:fixed;inset:0;background:rgba(0,0,0,.55);display:flex;align-items:center;justify-content:center;z-index:100}
.modal{background:var(--bg2);border:1px solid var(--bg4);border-radius:10px;padding:20px;width:460px}
.modal h3{font-size:14px;color:var(--brand);margin-bottom:14px}
.modal label{display:block;font-size:11px;color:var(--fg2);margin-bottom:4px;margin-top:10px}
.modal input[type=text]{width:100%;padding:5px 8px;background:var(--bg3);border:1px solid var(--bg4);border-radius:var(--radius);color:var(--fg);font-family:var(--mono);font-size:12px;outline:none}
.modal input[type=text]:focus{border-color:var(--brand)}
.modal .mf{display:flex;gap:8px;justify-content:flex-end;margin-top:16px}
.pbar{width:100%;height:5px;background:var(--bg4);border-radius:3px;overflow:hidden;margin:10px 0}
.pbar .fill{height:100%;background:var(--brand);border-radius:3px;transition:width .2s}
.ptxt{font-size:11px;color:var(--fg2);font-family:var(--mono)}
.cst{font-size:11px;font-weight:600;padding:1px 6px;border-radius:3px;text-transform:uppercase;letter-spacing:.3px}
.cst.added{color:var(--green);background:rgba(74,222,128,.1)}
.cst.removed{color:var(--red);background:rgba(248,113,113,.1)}
.cst.modified{color:var(--yellow);background:rgba(251,191,36,.1)}
.empty{text-align:center;padding:60px 20px;color:var(--fg3)}
.empty h3{color:var(--fg2);margin-bottom:4px;font-size:14px}
.spinner{display:inline-block;width:14px;height:14px;border:2px solid var(--bg4);border-top-color:var(--brand);border-radius:50%;animation:sp .5s linear infinite}
@keyframes sp{to{transform:rotate(360deg)}}
.ld{display:flex;align-items:center;gap:10px;padding:30px 20px;color:var(--fg3);font-size:12px}
::-webkit-scrollbar{width:7px;height:7px}
::-webkit-scrollbar-track{background:var(--bg)}
::-webkit-scrollbar-thumb{background:var(--bg4);border-radius:4px}
::-webkit-scrollbar-thumb:hover{background:var(--fg3)}
</style>
</head>
<body>
<div x-data="app()" x-init="init()" class="app">

  <!-- Header: DOOMSDAY : crumbs ---- actions -->
  <div class="hdr">
    <span class="logo">DOOMSDAY</span>
    <span class="sep">:</span>
    <div class="crumbs">
      <a @click="go('/')">destinations</a>
      <template x-if="currentDest"><span style="display:contents"><span class="sl">/</span>
        <a @click="go('/'+currentDest)" x-text="currentDest"></a>
      </span></template>
      <template x-if="page==='compare'"><span style="display:contents"><span class="sl">/</span><span class="leaf">compare</span></span></template>
      <template x-if="snapId"><span style="display:contents"><span class="sl">/</span>
        <a @click="go('/'+currentDest+'/'+snapId+'/files')" x-text="snapId"></a>
      </span></template>
      <template x-for="(seg,i) in crumbSegs" :key="i"><span style="display:contents"><span class="sl">/</span>
        <a x-show="i<crumbSegs.length-1||page!=='file'" @click="go(crumbSegURL(i))" x-text="seg"></a>
        <span x-show="i===crumbSegs.length-1&&page==='file'" class="leaf" x-text="seg"></span>
      </span></template>
    </div>
    <div class="actions">
      <input x-show="snapId" class="finput" type="text" placeholder="Search (*.pdf)" x-model="findPattern" @keydown.enter="doFind()" style="width:170px">
      <button x-show="cmpSel.length===2&&page==='snapshots'" class="btn" @click="doCompare()">Compare</button>
      <button x-show="page==='file'" class="btn" @click="downloadFile()">Download</button>
      <button x-show="snapId&&page!=='search'" class="btn primary" @click="openRestoreModal()">Restore</button>
    </div>
  </div>

  <!-- Main content area: exactly ONE view at a time, controlled by page -->
  <div class="main">
    <div x-show="page==='loading'" class="ld"><div class="spinner"></div> Loading...</div>

    <!-- destinations -->
    <table x-show="page==='destinations'" class="dt">
      <thead><tr><th>Destination</th><th>Type</th><th>Location</th><th>Status</th></tr></thead>
      <tbody><template x-for="d in destinations" :key="d.name">
        <tr @click="go('/'+d.name)">
          <td class="n"><span class="dot" :class="d.active?'g':'y'"></span><span x-text="d.name"></span></td>
          <td class="m" x-text="d.type"></td><td class="d" x-text="d.location"></td>
          <td><span x-text="d.active?'active':'inactive'" :style="'color:var(--'+(d.active?'green':'yellow')+')'"></span></td>
        </tr>
      </template></tbody>
    </table>

    <!-- snapshots -->
    <div x-show="page==='snapshots'">
      <table class="dt">
        <thead><tr><th style="width:30px"></th><th>ID</th><th>Date</th><th>Age</th><th>Host</th><th class="r">Files</th><th class="r">Size</th><th class="r">Added</th><th>Duration</th><th>Paths</th></tr></thead>
        <tbody><template x-for="s in snapshots" :key="s.id">
          <tr @click="go('/'+currentDest+'/'+s.short_id+'/files')">
            <td @click.stop><input type="checkbox" :checked="cmpSel.includes(s.id)" @change="toggleCmp(s.id,$event)" style="accent-color:var(--brand)"></td>
            <td class="m b" x-text="s.short_id"></td><td class="m" x-text="s.time"></td><td class="d" x-text="s.time_relative"></td>
            <td x-text="s.hostname"></td><td class="r m" x-text="s.total_files?.toLocaleString()||'-'"></td>
            <td class="r m" x-text="s.total_size_human||'-'"></td><td class="r m" x-text="s.data_added_human||'-'"></td>
            <td class="m" x-text="s.duration||'-'"></td>
            <td class="d" style="max-width:200px;overflow:hidden;text-overflow:ellipsis" x-text="s.paths"></td>
          </tr>
        </template></tbody>
      </table>
      <div x-show="snapshots.length===0" class="empty"><h3>No snapshots</h3></div>
    </div>

    <!-- compare -->
    <div x-show="page==='compare'">
      <table class="dt">
        <thead><tr><th>Status</th><th>File</th><th class="r" x-text="compareData?.snapshot_a||'A'"></th><th class="r" x-text="compareData?.snapshot_b||'B'"></th></tr></thead>
        <tbody><template x-for="e in (compareData?.entries||[])" :key="e.path">
          <tr>
            <td><span class="cst" :class="e.status" x-text="e.status"></span></td>
            <td class="n" x-text="e.path"></td>
            <td class="r m"><a x-show="e.size_a_human" @click="viewCompareFile(cmpIds[0],e.path)" x-text="e.size_a_human" style="cursor:pointer"></a></td>
            <td class="r m"><a x-show="e.size_b_human" @click="viewCompareFile(cmpIds[1],e.path)" x-text="e.size_b_human" style="cursor:pointer"></a></td>
          </tr>
        </template></tbody>
      </table>
      <div x-show="(compareData?.entries||[]).length===0" class="empty"><h3>No differences</h3></div>
    </div>

    <!-- search -->
    <div x-show="page==='search'">
      <div style="padding:6px 14px;font-size:11px;color:var(--fg3)" x-text="(searchResults||[]).length+' match(es)'+(searchTruncated?' (results capped)':'')"></div>
      <table class="dt">
        <thead><tr><th class="ic"></th><th>Path</th><th>Mode</th><th class="r">Size</th><th>Modified</th></tr></thead>
        <tbody><template x-for="r in (searchResults||[])" :key="r.path">
          <tr @click="openFoundFile(r)">
            <td class="ic" x-html="eIcon(r)"></td><td class="n" :class="{'dir':r.is_dir}" x-text="r.path"></td>
            <td class="m" x-text="r.mode"></td><td class="r m" x-text="r.is_dir?'-':r.size_human"></td><td class="m" x-text="r.mtime"></td>
          </tr>
        </template></tbody>
      </table>
      <div x-show="searchResults&&searchResults.length===0" class="empty"><h3>No matches</h3></div>
    </div>

    <!-- files (directory listing) -->
    <table x-show="page==='files'" class="dt">
      <thead><tr><th class="ic"></th><th>Name</th><th>Mode</th><th class="r">Size</th><th>Modified</th></tr></thead>
      <tbody>
        <tr x-show="browsingPath.length>0" @click="goUp()"><td class="ic">&#128193;</td><td class="n dir">..</td><td></td><td></td><td></td></tr>
        <template x-for="e in treeEntries" :key="e.name">
          <tr @click="clickEntry(e)">
            <td class="ic" x-html="eIcon(e)"></td>
            <td class="n" :class="{'dir':e.is_dir}"><span x-text="e.name"></span><span x-show="e.symlink_target" class="d"> &rarr; <span x-text="e.symlink_target"></span></span></td>
            <td class="m" x-text="e.mode"></td><td class="r m" x-text="e.is_dir?'-':e.size_human"></td><td class="m" x-text="e.mtime"></td>
          </tr>
        </template>
      </tbody>
    </table>

    <!-- file content -->
    <div x-show="page==='file'">
      <img x-show="fileData?.type==='image'" class="preview" :src="fURL('view')">
      <iframe x-show="fileData?.type==='pdf'" class="preview" :src="fileData?.type==='pdf'?fURL('view'):''"></iframe>
      <pre x-show="fileData?.type==='text'" x-html="fileData?.content"></pre>
      <div x-show="fileData?.type==='binary'" class="binary-msg"><div>Binary file</div><div class="sz" x-text="fileData?.size_human"></div>
        <button class="btn primary" @click="downloadFile()">Download</button></div>
    </div>
  </div>

  <!-- Restore modal -->
  <div x-show="showRestore" class="modal-bg" @click.self="!rStatus&&(showRestore=false)" style="display:none">
    <div class="modal">
      <h3>Restore</h3>
      <div style="font-size:12px;color:var(--fg2);margin-bottom:12px">
        <span class="m" style="color:var(--brand)" x-text="snapId"></span>
        <span x-show="restoreScope" class="d" x-text="' \u2192 '+restoreScope"></span>
      </div>
      <div x-show="!rStatus" style="margin-top:4px">
        <div style="padding:6px 0;cursor:pointer" @click="restoreMode='custom'">
          <div style="display:flex;align-items:center;gap:8px">
            <input type="radio" name="rmode" value="custom" x-model="restoreMode" style="accent-color:var(--brand);flex-shrink:0">
            <span style="font-size:12px">Restore to new location</span>
          </div>
          <div style="padding:4px 0 0 24px">
            <input class="finput" type="text" x-model="restoreTarget" @click.stop @focus="restoreMode='custom'" placeholder="/tmp/restore" style="width:100%">
          </div>
        </div>
        <div x-show="restoreOrigPath" style="padding:6px 0;cursor:pointer" @click="restoreMode='original'">
          <div style="display:flex;align-items:center;gap:8px">
            <input type="radio" name="rmode" value="original" x-model="restoreMode" style="accent-color:var(--brand);flex-shrink:0">
            <span style="font-size:12px;color:var(--yellow)">Restore to original location</span>
          </div>
          <div style="padding:2px 0 0 24px;font-size:11px;color:var(--fg3);overflow:hidden;text-overflow:ellipsis;white-space:nowrap" x-text="tp(restoreOrigDisplay||'',60)"></div>
        </div>
      </div>
      <div x-show="rStatus==='running'" style="margin-top:10px"><div class="pbar"><div class="fill" :style="'width:'+rPct+'%'"></div></div><div class="ptxt" x-text="rMsg" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap"></div></div>
      <div x-show="rStatus==='done'" style="color:var(--green);font-size:12px;margin:10px 0" x-text="rMsg"></div>
      <div x-show="rStatus==='error'||rStatus==='warning'" :style="'color:var(--'+(rStatus==='error'?'red':'yellow')+');font-size:12px;margin:10px 0'" x-text="rErr"></div>
      <div class="mf">
        <button class="btn" @click="showRestore=false;rStatus=null" x-text="rStatus==='done'||rStatus==='warning'?'Close':'Cancel'"></button>
        <button x-show="!rStatus" class="btn primary" @click="doRestore()" :disabled="restoreMode==='custom'&&!restoreTarget">Restore</button>
      </div>
    </div>
  </div>
</div>

<script>
function app(){
  // Strip token from URL on first load (cookie is set by server).
  const u=new URL(location);
  if(u.searchParams.has('token')){u.searchParams.delete('token');history.replaceState(null,'',u.pathname)}

  // --- API with LRU cache ---
  const _c=new Map();
  function _ck(p,q){return p+(Object.keys(q).length?'?'+new URLSearchParams(q):'')}
  function api(p,q={},cache){
    const k=_ck(p,q);
    if(cache&&_c.has(k))return _c.get(k); // return value directly, not a promise
    return fetch(k,{credentials:'same-origin'}).then(r=>r.ok?r.json():r.json().then(e=>{throw new Error(e.error||r.statusText)})).then(d=>{
      if(cache){_c.set(k,d);if(_c.size>50)_c.delete(_c.keys().next().value)}
      return d;
    })
  }
  function cached(p,q={}){return _c.has(_ck(p,q))}

  // --- URL scheme ---
  // /                              destinations
  // /dest                          snapshots
  // /dest/snap/files[/path...]     browse / view file
  // /dest/snap/search/pattern      search
  // /dest/compare/snapA/snapB      compare

  function toURL(dest,snap,mode,parts){
    if(!dest)return'/';
    let p='/'+enc(dest);
    if(mode==='compare')return p+'/compare/'+enc(parts[0]||'')+'/'+enc(parts[1]||'');
    if(!snap)return p;
    p+='/'+enc(snap);
    if(mode==='search')return p+'/search/'+enc(parts[0]||'');
    p+='/files';
    if(parts?.length)p+='/'+parts.map(enc).join('/');
    return p;
  }
  function enc(s){return encodeURIComponent(s)}

  function fromURL(path){
    const s=path.split('/').filter(Boolean).map(decodeURIComponent);
    if(!s[0])return{};
    if(s[1]==='compare')return{dest:s[0],mode:'compare',a:s[2],b:s[3]};
    if(!s[1])return{dest:s[0]};
    const snap=s[1];
    if(s[2]==='search')return{dest:s[0],snap,mode:'search',q:s.slice(3).join('/')};
    const start=s[2]==='files'?3:2;
    return{dest:s[0],snap,mode:'files',parts:s.slice(start)};
  }

  return {
    // --- State ---
    // page: exactly one of 'loading','destinations','snapshots','files','file','search','compare'
    page:'loading',
    destinations:[],
    currentDest:null, snapId:null, snapObj:null,
    snapshots:[], cmpSel:[], cmpIds:[null,null],
    treeEntries:[], browsingPath:[],
    fileData:null,
    compareData:null,
    searchResults:null, searchTruncated:false, findPattern:'',
    showRestore:false, restoreTarget:'/tmp/doomsday-restore',
    restoreMode:'custom', restoreOrigPath:null, restoreOrigDisplay:null, restoreScope:null,
    rStatus:null, rPct:0, rMsg:'', rErr:'',
    _noPush:false,

    // Computed: breadcrumb segments after the snapshot
    get crumbSegs(){
      if(this.page==='file')return[...this.browsingPath,this.fileData?.filename].filter(Boolean);
      return this.browsingPath;
    },
    crumbSegURL(i){
      const segs=this.browsingPath.slice(0,i+1);
      return toURL(this.currentDest,this.snapId,'files',segs);
    },

    // --- Init ---
    async init(){
      window.addEventListener('popstate',()=>this._nav(location.pathname,false));
      try{this.destinations=await api('/api/destinations')}catch(e){console.error(e)}
      await this._nav(location.pathname,false);
    },

    // --- Central navigation: go(path) drives everything ---
    go(path){this._nav(path,true)},

    async _nav(path,push){
      const s=fromURL(path);
      // Reset all view state atomically BEFORE any async work.
      this.fileData=null;this.compareData=null;this.searchResults=null;
      this.snapObj=null;this.snapId=null;this.treeEntries=[];this.browsingPath=[];

      // 1. No dest = destinations page.
      if(!s.dest){
        this.currentDest=null;this.snapshots=[];this.cmpSel=[];
        this.page='destinations';
        if(push)history.pushState(null,'','/');
        return;
      }

      // 2. Load snapshots if dest changed.
      if(this.currentDest!==s.dest){
        this.currentDest=s.dest;this.cmpSel=[];
        this.page='loading';
        try{this.snapshots=await api('/api/snapshots',{dest:s.dest})}
        catch(e){this.snapshots=[];console.error(e)}
      }

      // 3. Compare mode.
      if(s.mode==='compare'&&s.a&&s.b){
        const oa=this._findSnap(s.a),ob=this._findSnap(s.b);
        if(!oa||!ob){this.page='snapshots';return}
        this.cmpIds=[oa.id,ob.id];
        const q={dest:this.currentDest,a:oa.id,b:ob.id};
        if(!cached('/api/compare',q))this.page='loading';
        try{this.compareData=await api('/api/compare',q,true)}catch(e){console.error(e)}
        this.page='compare';
        if(push)history.pushState(null,'',toURL(this.currentDest,null,'compare',[oa.short_id,ob.short_id]));
        return;
      }

      // 4. No snapshot = snapshots page.
      if(!s.snap){
        this.page='snapshots';
        if(push)history.pushState(null,'',toURL(this.currentDest));
        return;
      }

      // 5. Resolve snapshot.
      const snapObj=this._findSnap(s.snap);
      if(!snapObj){this.page='snapshots';return}
      this.snapObj=snapObj;this.snapId=snapObj.short_id;

      // 6. Search mode.
      if(s.mode==='search'&&s.q){
        this.findPattern=s.q;
        const q={dest:this.currentDest,snapshot:snapObj.id,pattern:s.q};
        if(!cached('/api/find',q))this.page='loading';
        try{const d=await api('/api/find',q,true);this.searchResults=d.matches||[];this.searchTruncated=!!d.truncated}
        catch(e){this.searchResults=[]}
        this.page='search';
        if(push)history.pushState(null,'',toURL(this.currentDest,snapObj.short_id,'search',[s.q]));
        return;
      }

      // 7. File browsing - try loading as directory first.
      const parts=s.parts||[];
      this.browsingPath=parts.slice();
      const treeQ={dest:this.currentDest,snapshot:snapObj.id,path:'/'+this.browsingPath.join('/')};
      if(!cached('/api/tree',treeQ))this.page='loading';
      try{
        this.treeEntries=await api('/api/tree',treeQ,true);
        this.page='files';
        if(push)history.pushState(null,'',toURL(this.currentDest,snapObj.short_id,'files',this.browsingPath));
      }catch(e){
        // Last segment might be a file.
        if(this.browsingPath.length>0){
          const fn=this.browsingPath.pop();
          const parentQ={dest:this.currentDest,snapshot:snapObj.id,path:'/'+this.browsingPath.join('/')};
          try{
            this.treeEntries=await api('/api/tree',parentQ,true);
            await this._loadFile(fn,snapObj);
            if(push)history.pushState(null,'',toURL(this.currentDest,snapObj.short_id,'files',[...this.browsingPath,fn]));
          }catch(e2){this.treeEntries=[];this.page='files'}
        }else{this.page='files'}
      }
    },

    _findSnap(id){return this.snapshots.find(s=>s.short_id===id||s.id===id||s.id.startsWith(id))},

    async _loadFile(name,snapObj){
      const fp='/'+[...this.browsingPath,name].join('/');
      const ext=name.split('.').pop().toLowerCase();
      if(['png','jpg','jpeg','gif','svg','webp','bmp','ico'].includes(ext)){this.fileData={type:'image',filename:name,path:fp};this.page='file';return}
      if(ext==='pdf'){this.fileData={type:'pdf',filename:name,path:fp};this.page='file';return}
      this.page='loading';
      try{
        const d=await api('/api/file',{dest:this.currentDest,snapshot:snapObj.id,path:fp,mode:'view'});
        this.fileData=d.binary?{type:'binary',filename:d.filename,path:fp,size_human:hb(d.size)}
          :{type:'text',filename:d.filename,path:fp,content:d.content,lines:d.lines,truncated:d.truncated};
      }catch(e){console.error(e)}
      this.page='file';
    },

    // --- User actions ---
    goUp(){const bp=this.browsingPath.slice(0,-1);this.go(toURL(this.currentDest,this.snapId,'files',bp))},
    clickEntry(e){
      if(e.is_dir)this.go(toURL(this.currentDest,this.snapId,'files',[...this.browsingPath,e.name]));
      else if(e.has_content)this.go(toURL(this.currentDest,this.snapId,'files',[...this.browsingPath,e.name]));
    },
    doFind(){
      if(!this.findPattern||!this.snapObj)return;
      this.go(toURL(this.currentDest,this.snapId,'search',[this.findPattern]));
    },
    openFoundFile(r){
      if(r.type==='dir')return;
      const segs=r.path.replace(/^\//,'').split('/');
      this.go(toURL(this.currentDest,this.snapId,'files',segs));
    },
    toggleCmp(id,ev){
      if(ev.target.checked){if(this.cmpSel.length>=2){ev.target.checked=false;return}this.cmpSel.push(id)}
      else{this.cmpSel=this.cmpSel.filter(x=>x!==id)}
    },
    doCompare(){
      if(this.cmpSel.length!==2)return;
      const idxA=this.snapshots.findIndex(s=>s.id===this.cmpSel[0]);
      const idxB=this.snapshots.findIndex(s=>s.id===this.cmpSel[1]);
      const a=idxA>idxB?this.cmpSel[0]:this.cmpSel[1], b=idxA>idxB?this.cmpSel[1]:this.cmpSel[0];
      const sa=this._findSnap(a),sb=this._findSnap(b);
      this.go(toURL(this.currentDest,null,'compare',[sa.short_id,sb.short_id]));
    },
    viewCompareFile(snapId,filePath){
      const s=this._findSnap(snapId);if(!s)return;
      const segs=filePath.replace(/^\//,'').split('/');
      this.go(toURL(this.currentDest,s.short_id,'files',segs));
    },
    fURL(m){if(!this.fileData||!this.snapObj)return'';return'/api/file?'+new URLSearchParams({dest:this.currentDest,snapshot:this.snapObj.id,path:this.fileData.path,mode:m})},
    downloadFile(){if(!this.fileData||!this.snapObj)return;const a=document.createElement('a');a.href=this.fURL('download');a.download=this.fileData.filename;a.click()},
    openRestoreModal(){
      this.rStatus=null;this.rErr='';this.restoreMode='custom';
      this.restoreScope=this.fileData?this.fileData.path:this.browsingPath.length>0?'/'+this.browsingPath.join('/'):null;
      // With absolute paths in the tree, restore in place targets /.
      // Display the actual paths so user knows where files will go.
      const paths=this.snapObj?.paths_list||[];
      if(paths.length>0){
        this.restoreOrigPath='/';
        // Show source paths so user knows where files go back to.
        // For scoped restores (file/folder), show that specific path.
        this.restoreOrigDisplay=this.restoreScope||paths.join(', ');
      }else{this.restoreOrigPath=null;this.restoreOrigDisplay=null}
      this.restoreTarget='/tmp/doomsday-restore';
      this.showRestore=true;
    },
    async doRestore(){
      if(!this.snapObj)return;
      const target=this.restoreMode==='original'?this.restoreOrigPath:this.restoreTarget;
      if(!target)return;
      this.rStatus='running';this.rPct=0;this.rMsg='Starting...';this.rErr='';
      const p=new URLSearchParams({dest:this.currentDest,snapshot:this.snapObj.id,target});
      if(this.restoreScope)p.set('path',this.restoreScope);
      let lastUI=0,filesDone=0;
      try{
        const resp=await fetch('/api/restore?'+p,{credentials:'same-origin'});
        if(!resp.ok){let msg='Failed ('+resp.status+')';try{msg=(await resp.json()).error||msg}catch(_){}this.rErr=msg;this.rStatus='error';return}
        const reader=resp.body.getReader(),dec=new TextDecoder();
        let buf='',evtName='',gotDone=false;
        while(true){
          const{done,value}=await reader.read();if(done)break;
          buf+=dec.decode(value,{stream:true});const lines=buf.split('\n');buf=lines.pop();
          for(const line of lines){
            if(line.startsWith('event: ')){evtName=line.slice(7).trim()}
            else if(line.startsWith('data: ')&&evtName){
              try{const d=JSON.parse(line.slice(6));
                if(evtName==='progress'){filesDone=d.files_completed||0;const now=Date.now();if(now-lastUI>150||!d.path){lastUI=now;if(d.files_total>0)this.rPct=Math.round(filesDone/d.files_total*100);this.rMsg=filesDone+'/'+(d.files_total||0)+' files'+(d.path?' \u00b7 '+tp(d.path,40):'')}}
                else if(evtName==='restore_done'){this.rStatus='done';this.rPct=100;this.rMsg='Restored '+filesDone+' files'+(this.restoreMode==='original'?' to original location':' to '+d.target);gotDone=true}
                else if(evtName==='restore_error'){if(filesDone>0){this.rErr=d.error;this.rStatus='warning';this.rPct=100;this.rMsg='Restored '+filesDone+' files (with warnings)'}else{this.rErr=d.error||'Failed';this.rStatus='error'}return}
              }catch(pe){}evtName='';
            }else if(line==='')evtName='';
          }
        }
        if(!gotDone&&this.rStatus==='running'){if(filesDone>0){this.rStatus='done';this.rPct=100;this.rMsg='Restored '+filesDone+' files'}else{this.rErr='Stream ended unexpectedly';this.rStatus='error'}}
      }catch(e){if(this.rStatus==='running'){this.rErr=e.message||'Connection lost';this.rStatus='error'}}
    },
    eIcon(e){
      if(e.is_dir||e.type==='dir')return'&#128193;';const n=e.name||e.path||'',x=n.split('.').pop().toLowerCase();
      if(['png','jpg','jpeg','gif','svg','webp','bmp'].includes(x))return'\u{1F5BC}';
      if(x==='pdf')return'\u{1F4C4}';if(['zip','tar','gz','bz2','xz','7z','rar'].includes(x))return'\u{1F4E6}';
      if(e.type==='symlink')return'\u{1F517}';return'\u{1F4C4}';
    },
  };
}
function tp(p,n){if(p.length<=n)return p;const k=n-3,f=Math.floor(k/3),b=k-f;return p.slice(0,f)+'...'+p.slice(-b)}
function hb(b){if(b>=1099511627776)return(b/1099511627776).toFixed(1)+' TiB';if(b>=1073741824)return(b/1073741824).toFixed(1)+' GiB';if(b>=1048576)return(b/1048576).toFixed(1)+' MiB';if(b>=1024)return(b/1024).toFixed(1)+' KiB';return b+' B'}
</script>
<!--ALPINE_JS-->
</body>
</html>`
