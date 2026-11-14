package management

// startProManagementRuntime is the composition root for background features
// whose lifecycle follows one Management Handler.
func (h *Handler) startProManagementRuntime() {
	if h == nil {
		return
	}
	h.startAccountInspectionScheduler(accountInspectionQuotaAdapter{h: h})
	startRoutingPolicyController(h)
}

// Shutdown stops every background task owned by this management handler.
// It is safe to call more than once.
func (h *Handler) Shutdown() {
	if h == nil {
		return
	}
	h.shutdownOnce.Do(func() {
		if h.lifecycleCancel != nil {
			h.lifecycleCancel()
		}

		// Stop accepting usage before waiting for lifecycle goroutines. The
		// controller performs its own in-flight usage drain.
		stopRoutingPolicyController(h)

		if scheduler := schedulerForHandler(h); scheduler != nil {
			scheduler.shutdown()
			if scheduler.backupUnregister != nil {
				scheduler.backupUnregister()
			}
			if scheduler.backupHookUnregister != nil {
				scheduler.backupHookUnregister()
			}
		}
		h.lifecycleWG.Wait()
		accountInspectionSchedulers.Delete(h)
	})
}
