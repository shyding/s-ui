<template>
  <v-dialog v-model="localVisible" width="680" persistent>
    <v-card class="rounded-lg">
      <!-- 标题 -->
      <v-card-title class="d-flex align-center py-4 px-5">
        <v-icon start color="teal">mdi-shield-check</v-icon>
        <span>节点健康检测</span>
        <v-spacer />
        <v-chip v-if="isRunning" color="warning" variant="tonal" size="small" prepend-icon="mdi-loading">
          检测中…
        </v-chip>
        <v-chip v-else color="success" variant="tonal" size="small" prepend-icon="mdi-clock-outline">
          {{ nextSchedule }}
        </v-chip>
      </v-card-title>
      <v-divider />

      <v-card-text class="px-5 pt-4 pb-2">
        <!-- CPU 警告 -->
        <v-alert type="info" variant="tonal" density="compact" class="mb-4" icon="mdi-cpu-64-bit">
          <strong>低并发保护已启用</strong>：并发数 ≤ 5，不会导致系统 CPU 过高。
          每天凌晨 <strong>04:30</strong> 自动运行一次。
        </v-alert>

        <!-- 检测范围选择 -->
        <div class="text-subtitle-2 mb-2 text-medium-emphasis">选择检测范围</div>
        <v-row dense>
          <v-col cols="12" sm="6">
            <v-checkbox
              v-model="scope.egress"
              label="出口节点 (Egress / Proton / HProxy / Seed)"
              density="compact"
              hide-details
              color="teal"
            />
          </v-col>
          <v-col cols="12" sm="6">
            <v-checkbox
              v-model="scope.nodeHealth"
              label="外部订阅节点 (node_health_statuses)"
              density="compact"
              hide-details
              color="teal"
            />
          </v-col>
        </v-row>

        <v-divider class="my-3" />

        <!-- 状态卡片 -->
        <v-row dense class="mt-1">
          <v-col cols="6" sm="3">
            <v-card variant="tonal" color="teal" class="text-center pa-2">
              <div class="text-h6">{{ statusData.egressCheckRunning ? '运行中' : '空闲' }}</div>
              <div class="text-caption">出口检测</div>
            </v-card>
          </v-col>
          <v-col cols="6" sm="3">
            <v-card variant="tonal" color="indigo" class="text-center pa-2">
              <div class="text-h6">{{ statusData.nodeCheckRunning ? '运行中' : '空闲' }}</div>
              <div class="text-caption">节点检测</div>
            </v-card>
          </v-col>
          <v-col cols="12" sm="6">
            <v-card variant="tonal" color="grey" class="pa-2">
              <div class="text-caption text-medium-emphasis">下次定时</div>
              <div class="text-body-2">每天凌晨 04:30（北京时间）</div>
              <div class="text-caption text-medium-emphasis mt-1">并发限制</div>
              <div class="text-body-2">最多 5 个并发连接</div>
            </v-card>
          </v-col>
        </v-row>

        <!-- 结果提示 -->
        <v-alert
          v-if="resultMsg"
          :type="resultType"
          variant="tonal"
          density="compact"
          class="mt-3"
          closable
          @click:close="resultMsg = ''"
        >
          {{ resultMsg }}
        </v-alert>
      </v-card-text>

      <v-card-actions class="px-5 pb-4">
        <v-btn color="grey" variant="text" @click="close">关闭</v-btn>
        <v-spacer />
        <v-btn
          color="teal"
          variant="tonal"
          prepend-icon="mdi-refresh"
          :loading="triggering"
          :disabled="isRunning || (!scope.egress && !scope.nodeHealth)"
          @click="triggerCheck"
        >
          {{ isRunning ? '检测中，请稍候…' : '立即手动检测' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'

export default {
  props: ['visible'],
  emits: ['close'],
  data() {
    return {
      localVisible: false,
      triggering: false,
      resultMsg: '',
      resultType: 'success' as 'success' | 'error' | 'info',
      scope: {
        egress: true,
        nodeHealth: true,
      },
      statusData: {
        egressCheckRunning: false,
        nodeCheckRunning: false,
        anyRunning: false,
        nextSchedule: '每天凌晨 04:30',
      },
      pollTimer: null as ReturnType<typeof setInterval> | null,
    }
  },
  computed: {
    isRunning(): boolean {
      return this.statusData.anyRunning
    },
    nextSchedule(): string {
      return '04:30 定时'
    },
  },
  methods: {
    async fetchStatus() {
      try {
        const res = await HttpUtils.get('api/egressStatus')
        if (res && res.obj) {
          this.statusData = res.obj
        }
      } catch {
        // ignore
      }
    },
    async triggerCheck() {
      this.triggering = true
      this.resultMsg = ''
      try {
        // 根据 scope 选择不同的 API 参数
        const params: any = {
          egress: this.scope.egress ? '1' : '0',
          nodeHealth: this.scope.nodeHealth ? '1' : '0',
        }
        const res: any = await HttpUtils.post('api/egressHealthCheck', params)
        if (res && res.success) {
          this.resultType = 'success'
          this.resultMsg = '✅ 检测任务已在后台启动（低并发，不影响正常服务）'
          await this.fetchStatus()
        } else if (res && res.running === true && !res.success) {
          this.resultType = 'info'
          this.resultMsg = '⚠️ 已有检测任务正在运行，请稍后再试'
        } else {
          this.resultType = 'error'
          this.resultMsg = res?.msg || '触发失败，请查看日志'
        }
      } catch (e: any) {
        if (e?.response?.status === 409) {
          this.resultType = 'info'
          this.resultMsg = '⚠️ 已有检测任务正在运行，请稍后再试'
        } else {
          this.resultType = 'error'
          this.resultMsg = '请求失败：' + (e?.message || String(e))
        }
      }
      this.triggering = false
    },
    startPolling() {
      this.fetchStatus()
      this.pollTimer = setInterval(this.fetchStatus, 5000)
    },
    stopPolling() {
      if (this.pollTimer) {
        clearInterval(this.pollTimer)
        this.pollTimer = null
      }
    },
    close() {
      this.stopPolling()
      this.$emit('close')
    },
  },
  watch: {
    visible(val: boolean) {
      this.localVisible = val
      if (val) {
        this.resultMsg = ''
        this.startPolling()
      } else {
        this.stopPolling()
      }
    },
  },
}
</script>
