// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
	site: "https://www.echolocal.link",
	integrations: [
		starlight({
			title: "EchoLocal",
			description: "Home Assistant on supported Echos: voice, media, intercom, Bluetooth proxying, light effects, dashboards, apps and casting.",
			favicon: "/favicon.png",
			customCss: ['./src/styles/custom.css', './src/styles/accent.css'],
			head: [{ tag: 'script', attrs: { src: '/zoom.js', defer: true } }],
			components: { Hero: './src/components/ProjectHero.astro' },
			social: [{ icon: 'github', label: 'GitHub', href: "https://github.com/ygelfand/echolocal" }],
			sidebar: [
				{
					"label": "Explore EchoLocal",
					"items": [
						{
							"label": "Interface gallery",
							"slug": "use/gallery"
						},
						{
							"label": "Browser and remote control",
							"slug": "use/web"
						}
					]
				},
				{
					"label": "Get started",
					"items": [
						{
							"label": "Supported devices",
							"slug": "start/devices"
						},
						{
							"label": "Before you install",
							"slug": "start/before"
						},
						{
							"label": "Install EchoLocal",
							"slug": "start/install"
						},
						{
							"label": "Add it to Home Assistant",
							"slug": "start/home-assistant"
						},
						{
							"label": "The EchoLocal integration",
							"slug": "start/integration"
						}
					]
				},
				{
					"label": "Echo Dot",
					"items": [
						{
							"label": "Buttons and light ring",
							"slug": "use/dot"
						}
					]
				},
				{
					"label": "Echo Show",
					"items": [
						{
							"label": "Getting around",
							"slug": "use/getting-around"
						},
						{
							"label": "Dashboards",
							"slug": "use/dashboards"
						},
						{
							"label": "Styles and themes",
							"slug": "use/styles"
						},
						{
							"label": "Screen saver",
							"slug": "use/screen-saver"
						},
						{
							"label": "Visuals",
							"slug": "use/visuals"
						}
					]
				},
				{
					"label": "Features and apps",
					"items": [
						{
							"label": "Music and casting",
							"slug": "features/music"
						},
						{
							"label": "Bluetooth LE proxy",
							"slug": "features/bluetooth"
						},
						{
							"label": "Voice assistants",
							"slug": "features/voice"
						},
						{
							"label": "Microphones",
							"slug": "features/microphones"
						},
						{
							"label": "Intercom",
							"slug": "features/intercom"
						},
						{
							"label": "Messages, timers and alarms",
							"slug": "features/messages"
						},
						{
							"label": "Custom tones",
							"slug": "features/tones"
						},
						{
							"label": "Apps",
							"slug": "features/apps"
						},
						{
							"label": "Photos and wallpaper",
							"slug": "features/photos"
						},
						{
							"label": "Doorbell",
							"slug": "features/doorbell"
						},
						{
							"label": "Home alarm",
							"slug": "features/home-alarm"
						},
						{
							"label": "Thermostat",
							"slug": "features/thermostat"
						},
						{
							"label": "Camera",
							"slug": "features/camera"
						},
						{
							"label": "Privacy",
							"slug": "features/privacy"
						},
						{
							"label": "Updates",
							"slug": "features/updates"
						}
					]
				},
				{
					"label": "Configuration and reference",
					"collapsed": true,
					"items": [
						{
							"label": "Where to change settings",
							"slug": "use/configuration"
						},
						{
							"label": "On the screen",
							"slug": "use/settings"
						},
						{
							"label": "In Home Assistant",
							"slug": "use/entities"
						},
						{
							"label": "All settings and defaults",
							"slug": "use/reference"
						}
					]
				},
				{
					"label": "Help",
					"items": [
						{
							"label": "Questions and answers",
							"slug": "use/faq"
						}
					]
				}
			],
		}),
	],
});
