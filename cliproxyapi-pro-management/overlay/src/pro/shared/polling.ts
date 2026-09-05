// Schedule from completion: slow requests never overlap or starve their responses.
export function startPolling(task: () => Promise<void>, intervalMs: number) {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout>;
  const run = async () => {
    try {
      await task();
    } finally {
      if (!stopped)
        timer = setTimeout(() => {
          void run();
        }, intervalMs);
    }
  };
  timer = setTimeout(() => {
    void run();
  }, intervalMs);
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}
