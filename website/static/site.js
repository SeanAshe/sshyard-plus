// SSHYard website: appearance, language, downloads for this device, the
// demo window and the small interactions. No dependencies; the pages work
// without it.
(function () {
  "use strict";

  var doc = document.documentElement;
  var data = {};
  try { data = JSON.parse(document.getElementById("site-data").textContent); } catch (e) {}
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)");

  function store(key, value) {
    try {
      if (value === undefined) return localStorage.getItem(key);
      if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value);
    } catch (e) { return null; }
  }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  // ---------------------------------------------------------------- appearance

  var themeColors = { light: "#F5F5F7", dark: "#1E1E20" };
  var systemDark = window.matchMedia("(prefers-color-scheme: dark)");

  function applyTheme(mode) {
    if (mode === "light" || mode === "dark") doc.setAttribute("data-theme", mode);
    else doc.removeAttribute("data-theme");
    $$("[data-theme-set]").forEach(function (b) {
      b.setAttribute("aria-checked", String(b.getAttribute("data-theme-set") === (mode || "auto")));
    });
    var dark = mode === "dark" || (mode !== "light" && systemDark.matches);
    $$('meta[name="theme-color"]').forEach(function (m) {
      if (mode === "light" || mode === "dark") m.setAttribute("content", themeColors[dark ? "dark" : "light"]);
      else m.setAttribute("content", m.media.indexOf("dark") >= 0 ? themeColors.dark : themeColors.light);
    });
  }
  applyTheme(store("sshyard-theme"));
  $$("[data-theme-set]").forEach(function (b) {
    b.addEventListener("click", function () {
      var mode = b.getAttribute("data-theme-set");
      store("sshyard-theme", mode === "auto" ? null : mode);
      applyTheme(mode);
    });
  });
  $$(".theme-switch").forEach(function (group) {
    group.addEventListener("keydown", function (e) {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      var items = $$("[data-theme-set]", group);
      var i = items.indexOf(document.activeElement);
      if (i < 0) return;
      var next = items[(i + (e.key === "ArrowRight" ? 1 : items.length - 1)) % items.length];
      next.focus();
      next.click();
      e.preventDefault();
    });
  });

  // ---------------------------------------------------------------- menus

  function closeDetails(except) {
    $$("details.lang[open], details.menu[open]").forEach(function (d) { if (d !== except) d.open = false; });
  }
  document.addEventListener("click", function (e) {
    var inside = e.target.closest && e.target.closest("details.lang, details.menu");
    closeDetails(inside);
    if (e.target.closest && e.target.closest(".menu-panel a")) closeDetails();
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    var open = document.querySelector("details.lang[open], details.menu[open]");
    if (open) { open.open = false; open.querySelector("summary").focus(); }
  });

  var nav = document.querySelector(".nav");
  function onScroll() { if (nav) nav.classList.toggle("scrolled", window.scrollY > 4); }
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  // ---------------------------------------------------------------- language

  $$("[data-lang-pick]").forEach(function (a) {
    a.addEventListener("click", function () { store("sshyard-lang", a.getAttribute("data-lang-pick")); });
  });

  // The <head> script of base.html matches the browser's language and, on a
  // first visit to an English page, already redirected. Elsewhere (a shared
  // link into another language) only suggest the visitor's own.
  (function langBanner() {
    var banner = document.querySelector(".langbanner");
    if (!banner || !data.banner || store("sshyard-lang")) return;
    var want = doc.getAttribute("data-pref-lang") || "en";
    if (want === data.lang || !data.banner[want]) return;
    var alt = document.querySelector('link[rel="alternate"][hreflang="' + want + '"]');
    if (!alt) return;
    var b = data.banner[want];
    banner.setAttribute("lang", want);
    banner.querySelector(".langbanner-text").textContent = b.text;
    var go = banner.querySelector(".langbanner-go");
    go.textContent = b.switch;
    go.href = new URL(alt.href).pathname + location.hash;
    go.addEventListener("click", function () { store("sshyard-lang", want); });
    var close = banner.querySelector(".langbanner-close");
    close.setAttribute("aria-label", b.dismiss);
    close.title = b.dismiss;
    close.addEventListener("click", function () { store("sshyard-lang", data.lang); banner.hidden = true; });
    setTimeout(function () { banner.hidden = false; }, 900);
  })();

  // ---------------------------------------------------------------- downloads

  function detectOS() {
    var ua = navigator.userAgent || "";
    var p = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
    if (/iPhone|iPad|iPod|Android/i.test(ua) || (/Mac/i.test(p) && navigator.maxTouchPoints > 1)) return "";
    if (/Mac/i.test(p) || /Mac OS X/.test(ua)) return "macos";
    if (/Win/i.test(p) || /Windows/.test(ua)) return "windows";
    if (/Linux|X11|CrOS/i.test(p + " " + ua)) return "linux";
    return "";
  }

  // Apple Silicon unless the browser says Intel: high-entropy hints where
  // available, else the WebGL renderer (Safari reports "Apple GPU").
  // Windows and Linux are x64 unless the hints or the user agent say Arm
  // (x64 builds also run emulated on Windows on Arm).
  function arch(os) {
    var guess = os === "macos" ? function () {
      try {
        var gl = document.createElement("canvas").getContext("webgl");
        var ext = gl && gl.getExtension("WEBGL_debug_renderer_info");
        var r = ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : "";
        return /Intel|AMD|Radeon|NVIDIA/i.test(r) ? "amd64" : "arm64";
      } catch (e) { return "arm64"; }
    } : function () {
      return /aarch64|arm64|armv8/i.test(navigator.userAgent || "") ? "arm64" : "amd64";
    };
    var hints = navigator.userAgentData && navigator.userAgentData.getHighEntropyValues;
    if (!hints) return Promise.resolve(guess());
    return navigator.userAgentData.getHighEntropyValues(["architecture"]).then(function (v) {
      if (v.architecture === "x86") return "amd64";
      if (v.architecture === "arm") return "arm64";
      return guess();
    }, guess);
  }

  (function downloads() {
    if (!data.downloads) return;
    doc.setAttribute("data-os", "windows");
    arch("windows").then(function (a) {
      var arm = a === "arm64";
      var key = arm && data.downloads.win_setup_arm64 ? "win_setup_arm64" : "win_setup";
      if (!data.downloads[key]) return;
      $$("[data-dl-cta]").forEach(function (el) {
        var label = el.querySelector("[data-dl-label]");
        if (label && data.cta.windows) label.textContent = data.cta.windows;
        el.href = data.downloads[key].url;
      });
      var meta = document.querySelector("[data-dl-meta]");
      if (meta) {
        var d = data.downloads[key];
        meta.textContent = [data.labels[key], d.size].filter(Boolean).join(" · ");
      }
      // The visitor's architecture leads in their platform's card.
      $$('[data-platform="windows"] .dl-files').forEach(function (list) {
        var chips = $$("[data-asset]", list);
        var mine = chips.filter(function (x) { return /_arm64$/.test(x.getAttribute("data-asset")) === arm; });
        if (!mine.length || mine[0] === chips[0]) return;
        mine.concat(chips.filter(function (x) { return mine.indexOf(x) < 0; })).forEach(function (x, i) {
          list.appendChild(x);
          x.className = "btn " + (i === 0 ? "btn-primary" : "btn-quiet");
        });
      });
    });
  })();

  // ---------------------------------------------------------------- copy

  $$("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var text = btn.getAttribute("data-copy");
      var done = function () {
        btn.classList.add("done");
        btn.setAttribute("aria-label", data.copy.copied);
        btn.title = data.copy.copied;
        setTimeout(function () {
          btn.classList.remove("done");
          btn.setAttribute("aria-label", data.copy.copy);
          btn.title = data.copy.copy;
        }, 1600);
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done, function () {});
      } else {
        var ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); done(); } catch (e) {}
        ta.remove();
      }
    });
  });

  // ---------------------------------------------------------------- reveal

  var reveals = $$(".reveal");
  if ("IntersectionObserver" in window && !reduced.matches) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
      });
    }, { rootMargin: "0px 0px -8% 0px", threshold: 0.08 });
    reveals.forEach(function (el) { io.observe(el); });
  } else {
    reveals.forEach(function (el) { el.classList.add("in"); });
  }

  // ---------------------------------------------------------------- spotlight

  $$(".spot").forEach(function (el) {
    el.addEventListener("pointermove", function (e) {
      var r = el.getBoundingClientRect();
      el.style.setProperty("--mx", (e.clientX - r.left) + "px");
      el.style.setProperty("--my", (e.clientY - r.top) + "px");
    });
  });

  // ---------------------------------------------------------------- tabs

  // A tablist with roving focus; onSelect(index) shows the panel.
  function tabs(list, onSelect) {
    var items = $$('[role="tab"]', list);
    function select(i, focus) {
      items.forEach(function (t, j) {
        t.setAttribute("aria-selected", String(i === j));
        t.tabIndex = i === j ? 0 : -1;
      });
      if (focus) items[i].focus();
      onSelect(i);
    }
    items.forEach(function (t, i) {
      t.addEventListener("click", function () { select(i, false); list.dispatchEvent(new CustomEvent("picked")); });
    });
    list.addEventListener("keydown", function (e) {
      var i = items.indexOf(document.activeElement);
      if (i < 0) return;
      var n = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: items.length - 1 }[e.key];
      if (n === undefined) return;
      e.preventDefault();
      select((n + items.length) % items.length, true);
      list.dispatchEvent(new CustomEvent("picked"));
    });
    return { select: select, count: items.length, current: function () {
      return items.findIndex(function (t) { return t.getAttribute("aria-selected") === "true"; });
    } };
  }

  (function tour() {
    var root = document.querySelector(".tour");
    if (!root) return;
    var shots = $$(".shot", root);
    var list = root.querySelector(".tour-tabs");
    var ms = 7000;
    root.style.setProperty("--tour-ms", ms + "ms");
    shots.forEach(function (s, i) { if (i) s.setAttribute("aria-hidden", "true"); });
    var t = tabs(list, function (i) {
      shots.forEach(function (s, j) {
        s.classList.toggle("on", i === j);
        if (i === j) s.removeAttribute("aria-hidden"); else s.setAttribute("aria-hidden", "true");
      });
      if (shots[i + 1]) { var img = shots[i + 1].querySelector("img"); if (img) img.loading = "eager"; }
    });
    if (reduced.matches) return;
    var timer = null, visible = false, hover = false, stopped = false;
    function schedule() {
      clearTimeout(timer);
      root.classList.remove("playing");
      if (stopped || !visible || hover || document.hidden) { root.classList.toggle("paused", !stopped); return; }
      root.classList.remove("paused");
      void root.offsetWidth; // restart the progress animation
      root.classList.add("playing");
      timer = setTimeout(function () { t.select((t.current() + 1) % t.count, false); schedule(); }, ms);
    }
    list.addEventListener("picked", function () { stopped = true; schedule(); root.classList.remove("paused"); });
    var stage = root.querySelector(".tour-stage");
    stage.addEventListener("pointerenter", function () { hover = true; schedule(); });
    stage.addEventListener("pointerleave", function () { hover = false; schedule(); });
    document.addEventListener("visibilitychange", schedule);
    new IntersectionObserver(function (e) { visible = e[0].isIntersecting; schedule(); }, { threshold: 0.35 }).observe(stage);
  })();

  (function modes() {
    var list = document.querySelector(".mode-tabs");
    var card = document.querySelector(".approval");
    if (!list || !card) return;
    var panels = $$(".mode-text");
    var keys = $$('[role="tab"]', list).map(function (b) { return b.getAttribute("data-mode"); });
    tabs(list, function (i) {
      panels.forEach(function (p, j) { p.hidden = i !== j; });
      card.setAttribute("data-mode", keys[i]);
    });
  })();

  // ---------------------------------------------------------------- demo

  (function demo() {
    var root = document.querySelector(".demo");
    if (!root) return;
    var stats = {};
    $$("[data-stat]", root).forEach(function (el) { stats[el.getAttribute("data-stat")] = el; });
    if (reduced.matches) return;

    var visible = false;
    new IntersectionObserver(function (e) { visible = e[0].isIntersecting; }, { threshold: 0.2 }).observe(root);
    function idle() {
      return new Promise(function (resolve) {
        (function wait() { if (visible && !document.hidden) resolve(); else setTimeout(wait, 400); })();
      });
    }

    // Live-looking figures in the status bar.
    var cpu = 3, mem = 1.1, lat = 14;
    setInterval(function () {
      if (!visible) return;
      cpu = Math.max(1, Math.min(38, cpu + (Math.random() - 0.45) * 6));
      mem = Math.max(0.9, Math.min(1.4, mem + (Math.random() - 0.5) * 0.04));
      lat = Math.max(9, Math.min(31, lat + (Math.random() - 0.5) * 4));
      if (stats.cpu) stats.cpu.textContent = Math.round(cpu) + "%";
      if (stats.mem) stats.mem.textContent = mem.toFixed(1) + "G/1.6G";
      if (stats.up) stats.up.textContent = Math.round(200 + Math.random() * 900) + "B/s";
      if (stats.down) stats.down.textContent = (Math.random() * 3 + 0.4).toFixed(1) + "K/s";
      if (stats.lat) stats.lat.textContent = Math.round(lat) + " ms";
    }, 1500);

    var steps = $$("[data-at]", root);
    var typed = $$("[data-type], [data-type-at]", root);
    var streams = $$("[data-stream]", root);
    var cursor = root.querySelector(".cursor");
    typed.forEach(function (el) { el.setAttribute("data-text", el.textContent); });
    streams.forEach(function (el) { el.setAttribute("data-text", el.textContent); });
    var last = steps.reduce(function (m, el) { return Math.max(m, +el.getAttribute("data-at")); }, 0);

    function reset() {
      steps.forEach(function (el) { el.classList.add("pending"); });
      typed.forEach(function (el) { el.textContent = ""; });
    }
    function moveCursor(line) { if (cursor && line) line.appendChild(cursor); }
    function show(el) {
      el.classList.remove("pending");
      el.classList.add("entering");
      void el.offsetWidth;
      el.classList.remove("entering");
      if (el.classList.contains("tl")) moveCursor(el);
    }
    async function type(el) {
      var text = el.getAttribute("data-text");
      moveCursor(el.closest(".tl"));
      for (var i = 1; i <= text.length; i++) {
        await idle();
        el.textContent = text.slice(0, i);
        el.parentNode.appendChild(cursor);
        await sleep(28 + Math.random() * 55);
      }
      await sleep(260);
    }
    async function stream(el) {
      var chars = Array.from(el.getAttribute("data-text"));
      el.textContent = "";
      var cjk = /[぀-ヿ㐀-鿿가-힣]/.test(chars.join(""));
      for (var i = 0; i < chars.length;) {
        await idle();
        i += cjk ? 1 + Math.floor(Math.random() * 2) : 2 + Math.floor(Math.random() * 4);
        el.textContent = chars.slice(0, i).join("");
        await sleep(cjk ? 34 : 22);
      }
    }
    var pause = { 1: 300, 2: 500, 3: 900, 4: 700, 5: 900, 6: 500, 7: 900, 8: 900, 9: 500, 10: 300, 11: 400, 12: 0 };

    async function run() {
      await idle();
      for (;;) {
        reset();
        await sleep(700);
        for (var at = 1; at <= last; at++) {
          await idle();
          var here = steps.filter(function (el) { return +el.getAttribute("data-at") === at; });
          for (var k = 0; k < here.length; k++) {
            var el = here[k];
            if (el.hasAttribute("data-stream")) { show(el); await stream(el); }
            else {
              show(el);
              var cmd = el.querySelector("[data-type]");
              if (cmd) await type(cmd);
            }
          }
          var later = root.querySelector('[data-type-at="' + at + '"]');
          if (later) await type(later);
          await sleep(pause[at] || 500);
        }
        await sleep(6000);
      }
    }
    run();
  })();

  // ---------------------------------------------------------------- 404

  (function notFound() {
    var el = document.getElementById("notfound-data");
    if (!el) return;
    var all = JSON.parse(el.textContent);
    var path = location.pathname.slice((data.base || "").length);
    var code = Object.keys(all).find(function (c) { return all[c].prefix && path.indexOf("/" + all[c].prefix + "/") === 0; }) || "en";
    var t = all[code];
    if (code !== "en") doc.setAttribute("lang", code);
    $$("[data-nf]").forEach(function (n) { n.textContent = t[n.getAttribute("data-nf")]; });
    var home = document.querySelector('[data-nf="home"]');
    if (home) home.href = t.href;
  })();
})();
