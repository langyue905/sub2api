try {
    const saved = JSON.parse(localStorage.getItem("infinite-canvas:theme_store") || "{}");
    const theme = saved.state && saved.state.theme === "light" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.style.colorScheme = theme;
} catch {}
