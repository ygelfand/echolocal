const groups = {
	faces: {
		alt: (label) => `${label} clock face on the native dashboard`,
		labels: ['Plain', 'Stack', 'Cards', 'Analog', 'Segments', 'Words', 'Overlap', 'Analog seconds', 'Flip', 'Roman', 'Railway', 'Octagon', 'Atomic', 'Spoken', 'Math', 'Smiley', 'Neon', 'Bars', 'Reel', 'Binary', 'Nixie'],
	},
	placements: {
		alt: (label) => `${label} over Google Photos wallpaper`,
		labels: ['Small, bottom right', 'Medium, left', 'Medium, right'],
	},
	styles: {
		alt: (label) => `Tessera dashboard in the ${label} style`,
		values: ['standard', 'material', 'glass', 'eink', 'metro', 'terminal', 'hifi', 'neon', 'skeuo'],
		labels: ['Standard', 'Material You', 'Glass', 'E-ink', 'Metro', 'Terminal', 'Hi-fi', 'Neon', 'Skeuo'],
	},
	alerts: {
		alt: (label) => `Message displayed in the ${label} alert style`,
		labels: ['Banner', 'Card', 'Toast', 'Full screen'],
	},
	timers: {
		alt: (label) => `Running timer displayed in the ${label} style`,
		labels: ['Bar', 'Banner', 'Corner', 'Dial', 'Clock face', 'Full screen'],
	},
	homealarm: {
		alt: (label) => `Home alarm in the ${label} design with rehearsal data`,
		labels: ['Keypad', 'Shield', 'Card', 'Ring', 'Dial', 'Neon', 'Hardware', 'Flip board', 'Terminal', 'Console', 'Blueprint', 'Matrix', 'Vault', 'Minimal'],
		extension: 'webp',
		width: 1280,
		height: 800,
	},
	thermostat: {
		alt: (label) => `Thermostat in the ${label} look with rehearsal data`,
		labels: ['Dial', 'Column', 'Wheel', 'Bars', 'Split', 'Terminal', 'Brass', 'Mission'],
		extension: 'webp',
		width: 1280,
		height: 800,
	},
};

export const gallery = Object.fromEntries(Object.entries(groups).map(([name, group]) => [
	name,
	group.labels.map((label, index) => ({
		file: `${name}-${index}.${group.extension ?? 'png'}`,
		label,
		value: group.values?.[index] ?? label,
		alt: group.alt(label),
		width: group.width ?? 1280,
		height: group.height ?? 800,
	})),
]));
