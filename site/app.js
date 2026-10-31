// ccbs website: install tabs + copy, theme switching, the live preview.
(() => {
  const $ = (s) => document.querySelector(s);
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
  };

  // ── install command ─────────────────────────────────────────────
  // Build URLs from wherever this page is hosted, so the command is right
  // on any domain (and after the repo is renamed).
  const here = new URL('.', location.href).href;
  const CMDS = {
    unix: { sig: '$', cmd: `curl -fsSL ${here}install.sh | sh`,
      after: 'then run <code>ccbs</code> &nbsp;·&nbsp; installs to <code>~/.local/bin</code>, no sudo, checksum-verified' },
    win: { sig: 'PS>', cmd: `irm ${here}install.ps1 | iex`,
      after: 'then open a new terminal and run <code>ccbs</code> &nbsp;·&nbsp; no admin needed, checksum-verified' },
    go: { sig: '$', cmd: 'go install github.com/heyjeay/ccbm/cmd/ccbs@latest',
      after: 'needs Go 1.24+ &nbsp;·&nbsp; then run <code>ccbs</code>' },
  };
  const tabs = document.querySelectorAll('.tabs button');
  function pickOS(os) {
    tabs.forEach((b) => b.setAttribute('aria-selected', String(b.dataset.os === os)));
    $('#sig').textContent = CMDS[os].sig;
    $('#cmd').textContent = CMDS[os].cmd;
    $('#after').innerHTML = CMDS[os].after;
  }
  tabs.forEach((b) => b.addEventListener('click', () => pickOS(b.dataset.os)));
  pickOS(/Win/.test(navigator.platform || navigator.userAgent) ? 'win' : 'unix');

  $('#copy').addEventListener('click', async () => {
    const text = $('#cmd').textContent;
    let ok = false;
    try { await navigator.clipboard.writeText(text); ok = true; } catch {
      const ta = Object.assign(document.createElement('textarea'), { value: text });
      document.body.append(ta); ta.select();
      try { ok = document.execCommand('copy'); } catch { /* ignore */ }
      ta.remove();
    }
    const btn = $('#copy');
    btn.textContent = ok ? 'copied ✓' : 'select it ↑';
    btn.classList.toggle('done', ok);
    setTimeout(() => { btn.textContent = 'copy'; btn.classList.remove('done'); }, 1800);
  });

  // ── typed prompt ────────────────────────────────────────────────
  const typed = $('#typed');
  const word = 'ccbs';
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) typed.textContent = word;
  else [...word].forEach((ch, i) => setTimeout(() => { typed.textContent += ch; }, 500 + i * 140));

  // ── themes ──────────────────────────────────────────────────────
  const term = $('#term'), sel = $('#themeSel'), grid = $('#themeGrid');
  let themes = [], current = 0, auto = true, autoTimer = null, clockBase = null;
  const cache = new Map();
  const slug = (n) => n.toLowerCase();

  function applyVars(vars) {
    const root = document.documentElement.style;
    for (const [k, v] of Object.entries(vars)) {
      if (k === 'color-scheme') root.colorScheme = v; else root.setProperty(k, v);
    }
    document.querySelector('meta[name="theme-color"]').content = vars['--bg'];
  }

  async function frames(name) {
    if (!cache.has(name)) {
      cache.set(name, fetch(`themes/${slug(name)}.json`).then((r) => r.json()));
    }
    return cache.get(name);
  }

  const narrowQ = matchMedia('(max-width: 760px)');
  let shown = null;
  function paint() {
    if (!shown) return;
    const narrow = narrowQ.matches;
    term.innerHTML = narrow ? shown.narrow : shown.wide;
    fit(narrow ? 56 : 118);
    findClock();
  }
  // Scale the font so the whole frame fits the window, like a terminal would.
  function fit(cols) {
    const w = term.clientWidth - 16;
    const px = Math.max(5, Math.min(14, w / (cols * 0.602)));
    term.style.fontSize = px.toFixed(2) + 'px';
  }
  addEventListener('resize', () => { if (shown) fit(narrowQ.matches ? 56 : 118); });
  narrowQ.addEventListener?.('change', paint);

  // The frame was rendered at build time; keep its clock ticking from there.
  let clockEl = null;
  function findClock() {
    clockEl = [...term.querySelectorAll('span')].find((s) => /^\d\d:\d\d:\d\d$/.test(s.textContent));
    if (clockEl && !clockBase) {
      const [h, m, s] = clockEl.textContent.split(':').map(Number);
      clockBase = { secs: h * 3600 + m * 60 + s, at: Date.now() };
    }
  }
  setInterval(() => {
    if (!clockEl || !clockBase) return;
    const t = (clockBase.secs + Math.floor((Date.now() - clockBase.at) / 1000)) % 86400;
    const p = (n) => String(n).padStart(2, '0');
    clockEl.textContent = `${p(Math.floor(t / 3600))}:${p(Math.floor(t / 60) % 60)}:${p(t % 60)}`;
  }, 1000);

  async function setTheme(i, { user = false } = {}) {
    current = (i + themes.length) % themes.length;
    const t = themes[current];
    sel.value = t.name;
    grid.querySelectorAll('.chip').forEach((c, j) => c.setAttribute('aria-pressed', String(j === current)));
    applyVars(t.vars);
    term.classList.add('loading');
    try {
      const f = await frames(t.name);
      if (themes[current].name !== t.name) return; // superseded
      shown = f;
      paint();
    } finally { term.classList.remove('loading'); }
    if (user) {
      stopAuto();
      store.set('ccbs-theme', t.name);
    }
  }

  function stopAuto() {
    auto = false;
    clearInterval(autoTimer);
    $('#autoNote').textContent = 'Press t in ccbs to flip through themes the same way.';
  }

  // A few contrasting themes to show off while the visitor reads.
  const TOUR = ['Default', 'tokyo-night', 'gruvbox_dark', 'nord', 'dracula', 'everforest-dark-hard', 'HotPurpleTrafficLight', 'monokai'];

  fetch('themes/index.json').then((r) => r.json()).then((list) => {
    themes = list;
    sel.innerHTML = list.map((t) => `<option value="${t.name}">${t.name}</option>`).join('');
    grid.innerHTML = list.map((t, i) => {
      const v = t.vars;
      const css = `--cbg:${v['--bg']};--cfg:${v['--fg']};--ctitle:${v['--title']};--chi:${v['--hi']};--cbox1:${v['--box1']};--cg1:${v['--g1']};--cg2:${v['--g2']};--cg3:${v['--g3']}`;
      return `<button class="chip" style="${css}" data-i="${i}" aria-pressed="false">
        <span class="nm">${t.name}</span>
        <span class="sw"><i style="background:${v['--box1']}"></i><i style="background:${v['--box2']}"></i><i style="background:${v['--box3']}"></i><i style="background:${v['--box4']}"></i></span>
        <span class="bar"></span></button>`;
    }).join('');
    grid.addEventListener('click', (e) => {
      const chip = e.target.closest('.chip');
      if (chip) {
        setTheme(Number(chip.dataset.i), { user: true });
        document.querySelector('.preview-wrap').scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    });
    sel.addEventListener('change', () => setTheme(list.findIndex((t) => t.name === sel.value), { user: true }));
    $('#prev').addEventListener('click', () => setTheme(current - 1, { user: true }));
    $('#next').addEventListener('click', () => setTheme(current + 1, { user: true }));
    addEventListener('keydown', (e) => {
      if (e.target.closest('input,select,textarea') || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === 't') setTheme(current + 1, { user: true });
      if (e.key === 'T') setTheme(current - 1, { user: true });
    });

    const saved = store.get('ccbs-theme');
    const savedIdx = list.findIndex((t) => t.name === saved);
    if (savedIdx >= 0) {
      setTheme(savedIdx);
      stopAuto();
    } else {
      const tour = TOUR.map((n) => list.findIndex((t) => t.name === n)).filter((i) => i >= 0);
      let k = 0;
      setTheme(tour[0] ?? 0);
      if (!matchMedia('(prefers-reduced-motion: reduce)').matches) {
        autoTimer = setInterval(() => { if (auto) setTheme(tour[++k % tour.length]); }, 4500);
      } else stopAuto();
    }
  }).catch(() => {
    term.textContent = 'preview unavailable: run `go run ./cmd/sitegen` to build the theme frames.';
  });
})();
