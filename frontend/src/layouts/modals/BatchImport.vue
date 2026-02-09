<template>
  <v-dialog transition="dialog-bottom-transition" width="800" persistent>
    <v-card class="rounded-lg">
      <v-card-title>{{ $t('actions.batchImport') || 'Batch Import' }}</v-card-title>
      <v-divider></v-divider>
      <v-card-text>
        <v-container>
          <v-row>
            <v-col cols="12">
              <v-file-input
                ref="fileInput"
                v-model="selectedFile"
                :label="$t('batchImport.selectFile') || 'Select file (.txt)'"
                accept=".txt"
                prepend-icon="mdi-file-upload"
                variant="outlined"
                density="compact"
                @update:model-value="loadFile"
              ></v-file-input>
            </v-col>
          </v-row>
          <v-row>
            <v-col cols="12">
              <v-textarea
                v-model="links"
                :label="$t('batchImport.linksLabel') || 'Paste links (one per line)'"
                :placeholder="$t('batchImport.placeholder') || 'vless://...\nvmess://...\ntrojan://...\nss://...\nsocks5://...'"
                rows="10"
                auto-grow
                variant="outlined"
              ></v-textarea>
            </v-col>
          </v-row>
          <v-row v-if="result">
            <v-col cols="12">
              <v-alert :type="result.failed > 0 ? 'warning' : 'success'" variant="tonal">
                {{ $t('batchImport.result') || 'Result' }}: 
                {{ result.success }} {{ $t('batchImport.success') || 'success' }}, 
                {{ result.failed }} {{ $t('batchImport.failed') || 'failed' }}
              </v-alert>
              <v-list v-if="result.failedLinks && result.failedLinks.length > 0" density="compact">
                <v-list-subheader>{{ $t('batchImport.failedLinks') || 'Failed Links' }}</v-list-subheader>
                <v-list-item v-for="(link, i) in result.failedLinks" :key="i" class="text-error">
                  {{ link.substring(0, 80) }}...
                </v-list-item>
              </v-list>
            </v-col>
          </v-row>
        </v-container>
      </v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="primary" variant="outlined" @click="closeModal">
          {{ $t('actions.close') }}
        </v-btn>
        <v-btn color="primary" variant="tonal" :loading="loading" @click="importLinks">
          {{ $t('actions.import') || 'Import' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'
import Data from '@/store/modules/data'

interface ImportResult {
  success: number
  failed: number
  failedLinks: string[]
}

export default {
  props: ['visible'],
  emits: ['close'],
  data() {
    return {
      links: '',
      loading: false,
      result: null as ImportResult | null,
      selectedFile: null as File | null
    }
  },
  methods: {
    async importLinks() {
      if (!this.links.trim()) return
      
      this.loading = true
      this.result = null
      
      try {
        const response = await HttpUtils.post('api/batchImport', { links: this.links })
        if (response.success && response.obj) {
          this.result = response.obj
          // Refresh data after import
          if (this.result && this.result.success > 0) {
            await Data().loadData()
          }
        }
      } catch (error) {
        console.error('Import failed:', error)
      }
      
      this.loading = false
    },
    closeModal() {
      this.links = ''
      this.result = null
      this.selectedFile = null
      this.$emit('close')
    },
    loadFile() {
      if (this.selectedFile) {
        const reader = new FileReader()
        reader.onload = (e) => {
          if (e.target?.result) {
            this.links = e.target.result as string
          }
        }
        reader.readAsText(this.selectedFile)
      }
    }
  },
  watch: {
    visible(newValue) {
      if (newValue) {
        this.links = ''
        this.result = null
      }
    }
  }
}
</script>
