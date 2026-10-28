import http from 'http';
import path from 'path';
import { fileURLToPath } from 'url';
import { Client, GatewayIntentBits } from 'discord.js';
import {
  joinVoiceChannel,
  createAudioPlayer,
  createAudioResource,
  StreamType,
  AudioPlayerStatus,
  VoiceConnectionStatus
} from '@discordjs/voice';
import { Ytdl } from '@distube/ytdl-core';
import ytSearch from 'yt-search';
import ffmpegPath from 'ffmpeg-static';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Expose ffmpeg to PATH for prism-media
process.env.PATH = `${path.dirname(ffmpegPath)}:${process.env.PATH}`;

const token = process.env.DISCORD_BOT_TOKEN;
if (!token) {
  console.error('[Voice Worker] ❌ DISCORD_BOT_TOKEN is missing in environment!');
  process.exit(1);
}

const client = new Client({
  intents: [GatewayIntentBits.Guilds, GatewayIntentBits.GuildVoiceStates]
});

let connection = null;
let player = null;
let currentResource = null;
let currentGuildId = '1564174536029507635';
let currentChannelId = '1564174536591671350';
let currentVolume = 80;
let currentUrl = '';
let currentTitle = '';
let isManualStop = false;
let isSwitchingTrack = false;
const ytdlClient = new Ytdl();

function formatSec(s) {
  if (!s || isNaN(s)) return '03:30';
  const mins = Math.floor(s / 60);
  const secs = Math.floor(s % 60);
  return `${mins < 10 ? '0' : ''}${mins}:${secs < 10 ? '0' : ''}${secs}`;
}

function getOrCreatePlayer() {
  if (player) return player;
  player = createAudioPlayer();

  player.on(AudioPlayerStatus.Playing, () => {
    isSwitchingTrack = false;
    console.log(`🎶 [Voice Worker] Now playing audio in Discord: "${currentTitle}"`);
  });

  player.on(AudioPlayerStatus.Idle, async () => {
    console.log('[Voice Worker] ⏹️ Track finished or idle');
    if (isSwitchingTrack) {
      isSwitchingTrack = false;
      return;
    }
    if (isManualStop) {
      isManualStop = false;
      return;
    }

    // Natural finish: notify Go backend to skip to next queued song
    try {
      console.log(`⏩ [Voice Worker] Track finished naturally. Notifying Go backend for guild ${currentGuildId}...`);
      await fetch(`http://127.0.0.1:8080/api/guilds/${currentGuildId}/music/action`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'skip' })
      });
      console.log('⏩ [Voice Worker] Go backend notified successfully.');
    } catch (err) {
      console.warn('[Voice Worker] Could not notify Go backend:', err.message);
    }
  });

  player.on('error', (err) => {
    console.error('[Voice Worker] ⚠️ Audio Player error:', err.message);
  });

  return player;
}

function ensureVoiceConnection(guildId, channelId) {
  if (connection && connection.joinConfig.channelId === channelId && connection.state.status === VoiceConnectionStatus.Ready) {
    return connection;
  }

  const guild = client.guilds.cache.get(guildId);
  if (!guild) {
    console.error(`[Voice Worker] Guild ${guildId} not found in cache`);
    return null;
  }

  currentGuildId = guildId;
  currentChannelId = channelId;

  console.log(`🎙️ [Voice Worker] Connecting to voice channel ${channelId} in guild ${guildId}...`);
  connection = joinVoiceChannel({
    channelId,
    guildId,
    adapterCreator: guild.voiceAdapterCreator,
  });

  const p = getOrCreatePlayer();
  connection.subscribe(p);

  connection.on(VoiceConnectionStatus.Ready, () => {
    console.log(`🟢 [Voice Worker] Voice connection READY (DAVE E2EE active) in channel ${channelId}`);
  });

  connection.on('error', (err) => {
    console.error('[Voice Worker] ❌ Voice connection error:', err.message);
  });

  return connection;
}

function cleanYouTubeUrl(raw) {
  if (!raw) return '';
  try {
    const u = new URL(raw);
    const v = u.searchParams.get('v');
    if (v) return `https://www.youtube.com/watch?v=${v}`;
  } catch (e) {}
  return raw;
}

async function resolveAudioStreamAndMeta(query) {
  let url = cleanYouTubeUrl(query);
  let metadata = {
    title: query,
    artist: 'Warn-noi Audio Stream',
    duration_sec: 210,
    duration: '03:30',
    cover: 'https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=200&auto=format&fit=crop&q=80',
    url: query
  };

  console.log(`🔎 [Voice Worker] Searching YouTube with yt-search for: "${query}"...`);
  try {
    const r = await ytSearch(query);
    if (r && r.videos && r.videos.length > 0) {
      const v = r.videos[0];
      url = v.url;
      metadata.title = v.title;
      metadata.artist = v.author ? v.author.name : 'YouTube';
      metadata.duration_sec = v.seconds || 210;
      metadata.duration = v.timestamp || formatSec(v.seconds);
      metadata.cover = v.thumbnail || metadata.cover;
      metadata.url = v.url;
      console.log(`🎯 [Voice Worker] Resolved to: ${metadata.title} by ${metadata.artist} (${url})`);
    } else {
      console.log(`⚠️ [Voice Worker] No yt-search videos found for "${query}", using query directly`);
    }
  } catch (err) {
    console.warn(`⚠️ [Voice Worker] yt-search warning: ${err.message}`);
  }

  currentUrl = url;
  currentTitle = metadata.title;
  console.log(`📥 [Voice Worker] Downloading audio stream from: ${url}`);
  const stream = ytdlClient.download(url, {
    filter: { hasAudio: true, hasVideo: false },
    highWaterMark: 1 << 25
  });

  return { stream, metadata };
}

async function playTrack(guildId, channelId, songQuery, title) {
  isSwitchingTrack = true;
  if (title) currentTitle = title;
  const p = getOrCreatePlayer();
  ensureVoiceConnection(guildId, channelId);

  const { stream, metadata } = await resolveAudioStreamAndMeta(songQuery);

  currentResource = createAudioResource(stream, {
    inputType: StreamType.Arbitrary,
    inlineVolume: true
  });

  if (currentResource.volume) {
    currentResource.volume.setVolumeLogarithmic(currentVolume / 100);
  }

  p.play(currentResource);
  return { success: true, ...metadata };
}

// HTTP API Server on port 8081 for Go backend integration
const server = http.createServer(async (req, res) => {
  res.setHeader('Content-Type', 'application/json');

  if (req.method === 'POST') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', async () => {
      try {
        const data = body ? JSON.parse(body) : {};
        const urlPath = req.url;

        if (urlPath === '/play') {
          const gId = data.guild_id || currentGuildId;
          const cId = data.channel_id || currentChannelId;
          const query = data.url || data.query || 'https://www.youtube.com/watch?v=EIz09kLzN9k';
          const title = data.title || '';

          const result = await playTrack(gId, cId, query, title);
          res.writeHead(200);
          res.end(JSON.stringify(result));
          return;
        }

        if (urlPath === '/pause') {
          if (player) player.pause();
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, status: 'paused' }));
          return;
        }

        if (urlPath === '/resume') {
          if (player) player.unpause();
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, status: 'playing' }));
          return;
        }

        if (urlPath === '/stop') {
          isManualStop = true;
          if (player) player.stop();
          if (connection) {
            connection.destroy();
            connection = null;
          }
          currentTitle = '';
          currentUrl = '';
          console.log('⏹️ [Voice Worker] Stopped playback and disconnected from voice room.');
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, status: 'stopped_and_disconnected' }));
          return;
        }

        if (urlPath === '/volume') {
          const vol = parseInt(data.volume, 10);
          if (!isNaN(vol)) {
            currentVolume = Math.max(0, Math.min(100, vol));
            if (currentResource && currentResource.volume) {
              currentResource.volume.setVolumeLogarithmic(currentVolume / 100);
            }
          }
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, volume: currentVolume }));
          return;
        }

        if (urlPath === '/set_channel') {
          const gId = data.guild_id || currentGuildId;
          const cId = data.channel_id;
          if (cId) {
            ensureVoiceConnection(gId, cId);
          }
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, channel_id: cId }));
          return;
        }

        if (urlPath === '/disconnect') {
          isManualStop = true;
          if (player) player.stop();
          if (connection) {
            connection.destroy();
            connection = null;
          }
          res.writeHead(200);
          res.end(JSON.stringify({ success: true, status: 'disconnected' }));
          return;
        }

        res.writeHead(404);
        res.end(JSON.stringify({ error: 'Endpoint not found' }));
      } catch (err) {
        console.error('[Voice Worker API] Error:', err);
        res.writeHead(500);
        res.end(JSON.stringify({ success: false, error: err.message }));
      }
    });
    return;
  }

  if (req.method === 'GET' && req.url === '/status') {
    const isPlaying = player ? player.state.status === AudioPlayerStatus.Playing : false;
    res.writeHead(200);
    res.end(JSON.stringify({
      is_playing: isPlaying,
      channel_id: currentChannelId,
      volume: currentVolume,
      current_title: currentTitle,
      current_url: currentUrl
    }));
    return;
  }

  res.writeHead(404);
  res.end(JSON.stringify({ error: 'Not found' }));
});

const PORT = 8081;
client.once('ready', () => {
  console.log(`🤖 [Voice Worker] Discord Client connected as ${client.user.tag}`);
  server.listen(PORT, '127.0.0.1', () => {
    console.log(`🎧 [Voice Worker] Real Audio Microservice listening on http://127.0.0.1:${PORT}`);
  });
});

client.login(token);
