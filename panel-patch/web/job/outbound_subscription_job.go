package job

import (
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/websocket"
)

// OutboundSubscriptionJob periodically re-fetches enabled outbound subscriptions.
type OutboundSubscriptionJob struct {
	subService *service.OutboundSubscriptionService
	xraySvc    *service.XrayService
}

// NewOutboundSubscriptionJob creates the job.
func NewOutboundSubscriptionJob() *OutboundSubscriptionJob {
	return &OutboundSubscriptionJob{
		subService: &service.OutboundSubscriptionService{},
		xraySvc:    &service.XrayService{},
	}
}

// Run is invoked by the cron scheduler.
func (j *OutboundSubscriptionJob) Run() {
	if j.subService == nil {
		j.subService = &service.OutboundSubscriptionService{}
	}
	if j.xraySvc == nil {
		j.xraySvc = &service.XrayService{}
	}

	count, err := j.subService.RefreshAllEnabled()
	if err != nil {
		logger.Warning("outbound subscription auto-update error:", err)
		return
	}
	if count > 0 {
		logger.Infof("Refreshed %d outbound subscription(s)", count)
		port := j.xraySvc.GetXrayAPIPort()
		needRestart := j.subService.SyncAllRuntime(port)
		var legacy int64
		database.GetDB().Model(&model.OutboundSubscription{}).Where("enabled = ? AND (balancer_tag = '' OR balancer_tag IS NULL)", true).Count(&legacy)
		if needRestart || legacy > 0 {
			j.xraySvc.SetToNeedRestart()
		}
		websocket.BroadcastInvalidate(websocket.MessageTypeOutbounds)
	}
}
