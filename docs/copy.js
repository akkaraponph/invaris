const status = document.querySelector("#copy-status");

document.querySelectorAll(".snippet").forEach((snippet) => {
  const command = snippet.querySelector("pre code");
  const button = snippet.querySelector(".copy-button");
  button.hidden = false;

  button.addEventListener("click", async () => {
    let message = "Commands copied.";
    try {
      await navigator.clipboard.writeText(command.textContent);
    } catch {
      const range = document.createRange();
      range.selectNodeContents(command);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      message = "Commands selected. Use your keyboard to copy them.";
    }
    button.textContent = message.startsWith("Commands copied") ? "Copied" : "Selected";
    status.textContent = message;
    window.setTimeout(() => { button.textContent = "Copy"; status.textContent = ""; }, 2400);
  });
});
