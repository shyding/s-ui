<template>
  <v-dialog transition="dialog-bottom-transition" width="1200" persistent>
    <v-card class="rounded-lg">
      <v-card-title class="d-flex align-center">
        <span>{{ $t('nodeTest.title') || 'Node Test' }}</span>
        <v-spacer></v-spacer>
        <v-chip v-if="testing" color="primary" variant="tonal" class="mr-2">
          {{ $t('nodeTest.testing') || 'Testing...' }} {{ progress }}/{{ total }}
        </v-chip>
      </v-card-title>
      <v-divider></v-divider>
      <v-card-text style="max-height: 600px; overflow-y: auto;">
        <v-container>
          <!-- Controls -->
          <v-row class="mb-4">
            <v-col cols="12" sm="3">
              <v-select
                v-model="sortBy"
                :items="sortOptions"
                :label="$t('nodeTest.sortBy') || 'Sort By'"
                hide-details
                density="compact"
              ></v-select>
            </v-col>
            <v-col cols="12" sm="3">
              <v-select
                v-model="filterStatus"
                :items="filterOptions"
                :label="$t('nodeTest.filter') || 'Filter'"
                hide-details
                density="compact"
              ></v-select>
            </v-col>
            <v-col cols="12" sm="3">
              <v-text-field
                v-model="concurrency"
                :label="$t('nodeTest.concurrency') || 'Concurrency'"
                type="number"
                min="1"
                max="200"
                hide-details
                density="compact"
              ></v-text-field>
            </v-col>
            <v-col cols="12" sm="3">
              <v-checkbox
                v-model="queryIP"
                :label="$t('nodeTest.queryIP') || 'Query Landing IP'"
                hide-details
                density="compact"
              ></v-checkbox>
            </v-col>
          </v-row>

          <!-- Stats -->
          <v-row v-if="results.length > 0" class="mb-4">
            <v-col cols="6" sm="3">
              <v-card color="primary" variant="tonal">
                <v-card-text class="text-center">
                  <div class="text-h5">{{ results.length }}</div>
                  <div class="text-caption">{{ $t('nodeTest.total') || 'Total' }}</div>
                </v-card-text>
              </v-card>
            </v-col>
            <v-col cols="6" sm="3">
              <v-card color="success" variant="tonal">
                <v-card-text class="text-center">
                  <div class="text-h5">{{ availableCount }}</div>
                  <div class="text-caption">{{ $t('nodeTest.available') || 'Available' }}</div>
                </v-card-text>
              </v-card>
            </v-col>
            <v-col cols="6" sm="3">
              <v-card color="error" variant="tonal">
                <v-card-text class="text-center">
                  <div class="text-h5">{{ failedCount }}</div>
                  <div class="text-caption">{{ $t('nodeTest.failed') || 'Failed' }}</div>
                </v-card-text>
              </v-card>
            </v-col>
            <v-col cols="6" sm="3">
              <v-card color="info" variant="tonal">
                <v-card-text class="text-center">
                  <div class="text-h5">{{ avgLatency }}ms</div>
                  <div class="text-caption">{{ $t('nodeTest.avgLatency') || 'Avg Latency' }}</div>
                </v-card-text>
              </v-card>
            </v-col>
          </v-row>

          <!-- Results Table -->
          <v-data-table
            v-if="filteredResults.length > 0"
            :headers="currentHeaders"
            :items="filteredResults"
            :items-per-page="20"
            density="compact"
            class="elevation-1"
          >
            <template v-slot:item.available="{ item }">
              <v-chip
                :color="item.available ? 'success' : 'error'"
                size="small"
                variant="flat"
              >
                {{ item.available ? '✓' : '✗' }}
              </v-chip>
            </template>
            <template v-slot:item.latency="{ item }">
              <span :class="getLatencyClass(item.latency)">
                {{ item.latency >= 0 ? item.latency + 'ms' : '-' }}
              </span>
            </template>
            <template v-slot:item.landingIP="{ item }">
              <span v-if="item.landingIP">{{ item.landingIP }}</span>
              <span v-else class="text-grey">-</span>
            </template>
            <template v-slot:item.country="{ item }">
              <span v-if="item.country">{{ item.country }}</span>
              <span v-else class="text-grey">-</span>
            </template>
          </v-data-table>

          <v-alert v-else-if="!testing && results.length === 0" type="info" variant="tonal">
            {{ $t('nodeTest.clickStart') || 'Click "Start Test" to begin testing all nodes.' }}
          </v-alert>
        </v-container>
      </v-card-text>
      <v-card-actions>
        <v-btn
          color="warning"
          variant="tonal"
          @click="exportResults"
          :disabled="results.length === 0"
        >
          {{ $t('nodeTest.export') || 'Export' }}
        </v-btn>
        <v-spacer></v-spacer>
        <v-btn
          color="primary"
          variant="outlined"
          @click="closeModal"
          :disabled="testing"
        >
          {{ $t('actions.close') }}
        </v-btn>
        <v-btn
          color="primary"
          variant="tonal"
          :loading="testing"
          @click="startTest"
        >
          {{ queryIP ? ($t('nodeTest.testWithIP') || 'Test with IP') : ($t('nodeTest.startTest') || 'Start Test') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'

interface TestResult {
  tag: string
  server: string
  port: number
  latency: number
  available: boolean
  landingIP: string
  country: string
  region: string
  city: string
  isp: string
  error?: string
}

export default {
  props: ['visible'],
  emits: ['close'],
  data() {
    return {
      testing: false,
      results: [] as TestResult[],
      progress: 0,
      total: 0,
      concurrency: 50,
      queryIP: false,
      sortBy: 'latency',
      filterStatus: 'all',
      sortOptions: [
        { title: 'Latency (Low to High)', value: 'latency' },
        { title: 'Latency (High to Low)', value: 'latency-desc' },
        { title: 'Tag (A-Z)', value: 'tag' },
        { title: 'Status', value: 'status' }
      ],
      filterOptions: [
        { title: 'All', value: 'all' },
        { title: 'Available Only', value: 'available' },
        { title: 'Failed Only', value: 'failed' }
      ],
      basicHeaders: [
        { title: 'Status', key: 'available', width: '80px' },
        { title: 'Tag', key: 'tag' },
        { title: 'Server', key: 'server' },
        { title: 'Port', key: 'port', width: '80px' },
        { title: 'Latency', key: 'latency', width: '100px' }
      ],
      ipHeaders: [
        { title: 'Status', key: 'available', width: '70px' },
        { title: 'Tag', key: 'tag' },
        { title: 'Latency', key: 'latency', width: '90px' },
        { title: 'Landing IP', key: 'landingIP' },
        { title: 'Location', key: 'location' },
        { title: 'Error', key: 'error' }
      ]
    }
  },
  computed: {
    currentHeaders(): any[] {
      return this.queryIP ? this.ipHeaders : this.basicHeaders
    },
    availableCount(): number {
      return this.results.filter(r => r.available).length
    },
    failedCount(): number {
      return this.results.filter(r => !r.available).length
    },
    avgLatency(): number {
      const available = this.results.filter(r => r.available && r.latency > 0)
      if (available.length === 0) return 0
      const sum = available.reduce((acc, r) => acc + r.latency, 0)
      return Math.round(sum / available.length)
    },
    filteredResults(): any[] {
      let filtered = [...this.results].map(r => ({
        ...r,
        location: [r.country, r.region, r.city].filter(Boolean).join(' / ') || '-'
      }))
      
      // Filter
      if (this.filterStatus === 'available') {
        filtered = filtered.filter(r => r.available)
      } else if (this.filterStatus === 'failed') {
        filtered = filtered.filter(r => !r.available)
      }
      
      // Sort
      switch (this.sortBy) {
        case 'latency':
          filtered.sort((a, b) => {
            if (!a.available) return 1
            if (!b.available) return -1
            return a.latency - b.latency
          })
          break
        case 'latency-desc':
          filtered.sort((a, b) => {
            if (!a.available) return 1
            if (!b.available) return -1
            return b.latency - a.latency
          })
          break
        case 'tag':
          filtered.sort((a, b) => a.tag.localeCompare(b.tag))
          break
        case 'status':
          filtered.sort((a, b) => (b.available ? 1 : 0) - (a.available ? 1 : 0))
          break
      }
      
      return filtered
    }
  },
  methods: {
    async startTest() {
      this.testing = true
      this.results = []
      this.progress = 0
      
      try {
        const endpoint = this.queryIP ? 'api/testAllNodesWithIP' : 'api/testAllNodes'
        const response = await HttpUtils.post(endpoint, { 
          concurrency: this.concurrency.toString() 
        })
        
        if (response.success && response.obj) {
          this.results = response.obj
          this.total = this.results.length
          this.progress = this.total
        }
      } catch (error) {
        console.error('Test failed:', error)
      }
      
      this.testing = false
    },
    getLatencyClass(latency: number): string {
      if (latency < 0) return 'text-grey'
      if (latency < 100) return 'text-success'
      if (latency < 300) return 'text-warning'
      return 'text-error'
    },
    exportResults() {
      let csv: string
      let header: string
      
      if (this.queryIP) {
        csv = this.filteredResults.map(r => 
          `${r.tag},${r.server},${r.port},${r.available ? 'OK' : 'FAIL'},${r.latency}ms,${r.landingIP || ''},${r.country || ''}`
        ).join('\n')
        header = 'Tag,Server,Port,Status,Latency,LandingIP,Country\n'
      } else {
        csv = this.filteredResults.map(r => 
          `${r.tag},${r.server},${r.port},${r.available ? 'OK' : 'FAIL'},${r.latency}ms`
        ).join('\n')
        header = 'Tag,Server,Port,Status,Latency\n'
      }
      
      const blob = new Blob([header + csv], { type: 'text/csv' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'node-test-results.csv'
      a.click()
      URL.revokeObjectURL(url)
    },
    closeModal() {
      this.$emit('close')
    }
  },
  watch: {
    visible(newValue) {
      if (newValue) {
        this.results = []
        this.progress = 0
      }
    }
  }
}
</script>
