const isLocal = window.location.protocol === "file:";
const downloadUrl = isLocal ? "mesh.zip" : "/download/mesh.zip";

// Insert download link into the menu when it exists
const observer = new MutationObserver(() => {
  const menu = document.getElementById("menu-panel");
  if (menu) {
    observer.disconnect();
    const link = document.createElement("a");
    link.href = downloadUrl;
    link.textContent = "Download";
    link.id = "download-link";
    menu.appendChild(link);
  }
});
observer.observe(document.body, { childList: true, subtree: true });
