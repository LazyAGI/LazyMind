// Keep this entry free of application imports so failures evaluating an app chunk
// can still reveal the static recovery page supplied by index.html.
void import('./main').catch(() => {
  document.dispatchEvent(new Event('lazymind:startup-failed'));
});
