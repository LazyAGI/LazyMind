import CurrentMemoryIdentitySection from "../CurrentMemoryIdentitySection";
import EpisodeMemorySection from "../EpisodeMemorySection";
import PreferenceMemorySection from "../PreferenceMemorySection";

export default function ExperienceOverview() {
  return (
    <div className="memory-experience-overview">
      <CurrentMemoryIdentitySection />
      <div className="memory-experience-detail-grid">
        <PreferenceMemorySection />
        <EpisodeMemorySection />
      </div>
    </div>
  );
}
